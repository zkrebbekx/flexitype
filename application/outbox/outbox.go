// Package outbox implements at-least-once event delivery: the unit of
// work writes envelopes into an outbox table in the same transaction as
// the change, and a relay dispatches them to the registered hooks,
// retrying on failure. Without the outbox, a crash between commit and
// dispatch loses events; with it, every consumer (webhooks, pub/sub, the
// search indexer) sees each committed change at least once.
package outbox

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Store is the persistence port for the outbox.
type Store interface {
	// Write persists envelopes inside the caller's transaction — the unit
	// of work's pre-commit handler.
	Write(ctx context.Context, tx db.QueryExecer, envs []events.Envelope) error

	// Claim leases up to limit undispatched envelopes for the given relay
	// and returns them for dispatch. Claiming takes a short row lease
	// (claimed_by/claimed_at) so no other relay grabs the same rows while
	// this relay dispatches them outside any transaction; a lease older
	// than leaseTTL is treated as abandoned (crashed relay) and reclaimed.
	// It does NOT hold the sequencer lock — no network I/O happens here.
	Claim(ctx context.Context, relayID string, limit int, leaseTTL time.Duration) ([]events.Envelope, error)

	// Finalize records the outcome of dispatching a claimed batch. Under
	// the single-sequencer advisory lock (DB-only, no network I/O) it
	// assigns feed_seq to each success in claim order, fans out one
	// webhook-delivery row per matching subscription and marks the
	// envelope dispatched; failures have their attempt counted and their
	// lease cleared so a later pass retries them.
	Finalize(ctx context.Context, results []Result) error
}

// Result records one dispatch attempt.
type Result struct {
	EnvelopeID string
	Err        error
}

// Relay drains the outbox: on a nudge (post-commit) or on the interval it
// fetches pending envelopes, dispatches them and records outcomes. Failed
// envelopes stay pending and retry on later passes.
type Relay struct {
	store       Store
	dispatcher  *events.Dispatcher
	interval    time.Duration
	batch       int
	leaseTTL    time.Duration
	relayID     string
	nudge       chan struct{}
	onError     func(err error)
	afterExpand func()
}

// RelayOption customises a Relay.
type RelayOption func(*Relay)

// WithInterval sets the poll interval (default 2s).
func WithInterval(d time.Duration) RelayOption {
	return func(r *Relay) { r.interval = d }
}

// WithBatchSize sets how many envelopes one pass claims (default 100).
func WithBatchSize(n int) RelayOption {
	return func(r *Relay) { r.batch = n }
}

// WithErrorObserver receives relay-level failures (fetch errors — dispatch
// failures are recorded per envelope).
func WithErrorObserver(fn func(err error)) RelayOption {
	return func(r *Relay) { r.onError = fn }
}

// WithAfterExpand installs a hook invoked after a pass that expanded
// envelopes — the delivery worker's nudge, keeping webhook latency in
// milliseconds.
func WithAfterExpand(fn func()) RelayOption {
	return func(r *Relay) { r.afterExpand = fn }
}

// WithLeaseTTL sets how long a claimed batch stays leased to this relay
// before another relay may reclaim it (default 1m). It should comfortably
// exceed the slowest expected dispatch of one batch.
func WithLeaseTTL(d time.Duration) RelayOption {
	return func(r *Relay) { r.leaseTTL = d }
}

// WithRelayID sets the identifier stamped on leases (default a random
// ULID). Only useful for deterministic tests.
func WithRelayID(id string) RelayOption {
	return func(r *Relay) { r.relayID = id }
}

// NewRelay builds a relay over the store and dispatcher.
func NewRelay(store Store, dispatcher *events.Dispatcher, opts ...RelayOption) *Relay {
	r := &Relay{
		store:      store,
		dispatcher: dispatcher,
		interval:   2 * time.Second,
		batch:      100,
		leaseTTL:   time.Minute,
		relayID:    ulid.New().String(),
		nudge:      make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Nudge wakes the relay immediately — called post-commit so delivery
// latency stays milliseconds in the happy path.
func (r *Relay) Nudge() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// Run drains the outbox until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-r.nudge:
		case <-ticker.C:
		}
	}
}

// DrainOnce performs one drain pass — claiming, dispatching and finalizing
// batches until the outbox is empty. Run loops these on a nudge or the
// interval; DrainOnce is exposed for one-shot draining and tests.
func (r *Relay) DrainOnce(ctx context.Context) { r.drain(ctx) }

// drain processes batches until the outbox is empty or ctx ends. Each pass
// leases a batch, dispatches it to the in-process handlers OUTSIDE any lock
// or transaction, then finalizes the outcomes under the sequencer lock. A
// slow or failing handler therefore never holds the expansion lock or a
// database connection across its network I/O.
func (r *Relay) drain(ctx context.Context) {
	expanded := false
	for ctx.Err() == nil {
		envs, err := r.store.Claim(ctx, r.relayID, r.batch, r.leaseTTL)
		if err != nil {
			r.report(err)
			break
		}
		if len(envs) == 0 {
			break
		}

		results := make([]Result, 0, len(envs))
		for _, env := range envs {
			results = append(results, Result{
				EnvelopeID: env.ID,
				Err:        r.dispatcher.DispatchEnvelopes(ctx, env),
			})
		}

		if err := r.store.Finalize(ctx, results); err != nil {
			r.report(err)
			break
		}
		expanded = true
		if len(envs) < r.batch {
			break
		}
	}
	if expanded && r.afterExpand != nil {
		r.afterExpand()
	}
}

func (r *Relay) report(err error) {
	if r.onError != nil {
		r.onError(err)
	}
}
