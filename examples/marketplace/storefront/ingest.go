package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/zkrebbekx/flexitype/pkg/events"
)

// maxDeliveryBytes bounds one webhook body. An unbounded read is a cheap way
// to make this process allocate arbitrary memory before a signature is even
// checked.
const maxDeliveryBytes = 1 << 20

// signatureTolerance is how far a delivery's timestamp may be from now. It
// bounds a replay of a captured, correctly signed delivery.
const signatureTolerance = 5 * time.Minute

// Ingest receives signed flexitype webhooks and turns each one into a
// projection request.
//
// The delivery URL carries the tenant — /hook/{tenant} — so one HMAC
// verification is enough whatever the number of merchants. The path is
// UNTRUSTED input: it only selects which secret to try, and a wrong tenant
// therefore fails verification. After verification the tenant is taken from
// the SIGNED envelope, and a mismatch with the path is rejected.
type Ingest struct {
	store     *Store
	debouncer *Debouncer
	seen      *seenSet
	log       Logger
	now       func() time.Time
}

// NewIngest wires the webhook receiver.
func NewIngest(store *Store, debouncer *Debouncer, log Logger) *Ingest {
	return &Ingest{
		store:     store,
		debouncer: debouncer,
		seen:      newSeenSet(10000),
		log:       log,
		now:       time.Now,
	}
}

// valueEventPayload is the part of a value event the projector needs. Only the
// entity's coordinates are read; the value itself is deliberately ignored,
// because the projector re-reads it (see Projector).
type valueEventPayload struct {
	TypeDefinitionID string `json:"type_definition_id"`
	EntityID         string `json:"entity_id"`
}

// ServeHTTP handles one delivery.
func (i *Ingest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")

	body, err := io.ReadAll(io.LimitReader(r.Body, maxDeliveryBytes))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	merchant, ok, err := i.store.Merchant(r.Context(), tenant)
	if err != nil {
		i.log.Error("look up merchant for delivery", "tenant", tenant, "error", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	// An unknown tenant answers exactly like a bad signature. A different
	// answer would let an unauthenticated caller enumerate which merchants
	// this storefront serves.
	secrets := []string{}
	if ok {
		secrets = append(secrets, merchant.WebhookSecret)
	}

	ts := r.Header.Get(events.HeaderTimestamp)
	sig := r.Header.Get(events.HeaderSignature)
	if !events.VerifyRequest(secrets, ts, body, sig, signatureTolerance, i.now()) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var env events.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	// The signed envelope is the authority on the tenant. The path only chose
	// a secret; a delivery signed for one merchant must never be attributed
	// to another.
	if env.TenantID != tenant {
		http.Error(w, "tenant mismatch", http.StatusUnauthorized)
		return
	}

	// Delivery is at-least-once, so a redelivery must be a no-op. The
	// projector is idempotent anyway; this only saves the work.
	if !i.seen.markNew(env.ID) {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Only value events move a product. A type or attribute definition change
	// alters the schema, not the catalog, and the schema cache picks it up.
	if env.AggregateType == "attribute_value" {
		var payload valueEventPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil || payload.EntityID == "" {
			i.log.Error("unreadable value event payload", "event", env.ID)
			// A 2xx: retrying will not make this payload parse, and a 4xx
			// would send the delivery to the dead-letter queue for a bug on
			// this side.
			w.WriteHeader(http.StatusOK)
			return
		}
		i.debouncer.Trigger(entityKey{Tenant: tenant, TypeID: payload.TypeDefinitionID, EntityID: payload.EntityID})
	}

	// Acknowledge fast. The projection runs after the debounce window, off
	// this goroutine, so a slow re-read never trips the sender's retry.
	w.WriteHeader(http.StatusOK)
}

// Debouncer coalesces a burst of events for one entity into one projection.
//
// Writing a whole product is one batch of value events — eight fields is eight
// deliveries. Projecting each one would re-read the same entity eight times
// and write the same row eight times. The first event for an entity opens a
// window; every event inside it is absorbed, and one projection runs when the
// window closes.
type Debouncer struct {
	delay   time.Duration
	project func(ctx context.Context, key entityKey) error
	log     Logger

	mu      sync.Mutex
	pending map[entityKey]bool
	wg      sync.WaitGroup
}

// NewDebouncer builds a debouncer. A delay of zero projects inline, which is
// what a test wants: no timer to wait on.
func NewDebouncer(delay time.Duration, project func(ctx context.Context, key entityKey) error, log Logger) *Debouncer {
	return &Debouncer{delay: delay, project: project, log: log, pending: map[entityKey]bool{}}
}

// Trigger schedules a projection for key.
func (d *Debouncer) Trigger(key entityKey) {
	if d.delay <= 0 {
		d.run(key)
		return
	}
	d.mu.Lock()
	if d.pending[key] {
		// A window is already open for this entity; this event joins it.
		d.mu.Unlock()
		return
	}
	d.pending[key] = true
	d.mu.Unlock()

	d.wg.Add(1)
	time.AfterFunc(d.delay, func() {
		defer d.wg.Done()
		// Clear the pending mark BEFORE projecting. A value written while the
		// projection is running then opens a new window instead of being
		// swallowed by the one that is closing.
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()
		d.run(key)
	})
}

func (d *Debouncer) run(key entityKey) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.project(ctx, key); err != nil {
		// A failed projection is logged rather than retried here: the next
		// event for this entity reprojects it, and the backfill repairs
		// anything that stayed stale.
		d.log.Error("project entity", "entity", key.String(), "error", err)
	}
}

// Wait blocks until every scheduled projection has run. Shutdown calls it so
// an in-flight window is not lost.
func (d *Debouncer) Wait() { d.wg.Wait() }

// seenSet records handled event ids so an at-least-once redelivery is a no-op.
//
// Deliveries arrive on one goroutine each, so the map needs a lock, and the
// check and the mark are one step so two concurrent redeliveries of one event
// cannot both look new. It is bounded: a process that ran for a month would
// otherwise hold every event id it had ever seen.
type seenSet struct {
	mu    sync.Mutex
	ids   map[string]bool
	order []string
	limit int
}

func newSeenSet(limit int) *seenSet {
	return &seenSet{ids: make(map[string]bool, limit), limit: limit}
}

// markNew records id and reports whether it was new.
func (s *seenSet) markNew(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[id] {
		return false
	}
	s.ids[id] = true
	s.order = append(s.order, id)
	if len(s.order) > s.limit {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.ids, oldest)
	}
	return true
}
