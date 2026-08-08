package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// outboxStore persists and claims outbox envelopes.
type outboxStore struct {
	tx db.Transactor // pool-level transactor for relay claims
	// maxAttempts parks a row that has failed this many times, so an
	// undeliverable envelope stops being retried and becomes visible as a
	// terminal state rather than as permanent load.
	maxAttempts int
	// retryCeiling caps the exponential backoff between attempts.
	retryCeiling time.Duration
}

// Default retry scheduling for the outbox lane, matching the delivery lane's
// shape: 1s, 4s, 16s, 64s, 256s, then the ceiling.
const (
	defaultOutboxMaxAttempts  = 25
	defaultOutboxRetryCeiling = 15 * time.Minute
)

// NewOutboxStore builds the outbox adapter over the pool transactor.
func NewOutboxStore(tx db.Transactor, opts ...OutboxStoreOption) outbox.Store {
	s := &outboxStore{
		tx:           tx,
		maxAttempts:  defaultOutboxMaxAttempts,
		retryCeiling: defaultOutboxRetryCeiling,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// OutboxStoreOption customises the outbox adapter's retry scheduling.
type OutboxStoreOption func(*outboxStore)

// WithOutboxMaxAttempts sets how many failures park a row. Non-positive
// values are ignored.
func WithOutboxMaxAttempts(n int) OutboxStoreOption {
	return func(s *outboxStore) {
		if n > 0 {
			s.maxAttempts = n
		}
	}
}

// WithOutboxRetryCeiling caps the backoff between attempts. Non-positive
// values are ignored.
func WithOutboxRetryCeiling(d time.Duration) OutboxStoreOption {
	return func(s *outboxStore) {
		if d > 0 {
			s.retryCeiling = d
		}
	}
}

func (s *outboxStore) Write(ctx context.Context, tx db.Tx, envs []events.Envelope) error {
	if len(envs) == 0 {
		return nil
	}
	q := txExecer(tx)

	const cols = 11
	rows := make([]string, 0, len(envs))
	args := make([]any, 0, len(envs)*cols)
	for _, env := range envs {
		rows = append(rows, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			env.ID, env.TenantID, env.Actor, env.Type.String(), env.AggregateType,
			env.AggregateID, jsonbParam(env.Payload), env.OccurredAt, env.RecordedAt,
			// Empty rather than NULL for an event that concerns no single
			// entity, so a read never has to handle both.
			env.TypeDefinitionID, env.EntityID,
		)
	}

	query := bind(`INSERT INTO flexitype_event_outbox
	   (id, tenant_id, actor, event_type, aggregate_type, aggregate_id, payload, occurred_at, recorded_at,
	    type_definition_id, entity_id)
	 VALUES ` + strings.Join(rows, ", "))
	if _, err := q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("write outbox: %w", err)
	}
	return nil
}

type outboxRow struct {
	ID            ulid.ID   `db:"id"`
	TenantID      string    `db:"tenant_id"`
	Actor         string    `db:"actor"`
	EventType     string    `db:"event_type"`
	AggregateType string    `db:"aggregate_type"`
	AggregateID   string    `db:"aggregate_id"`
	Payload       string    `db:"payload"`
	OccurredAt    time.Time `db:"occurred_at"`
	RecordedAt    time.Time `db:"recorded_at"`
	// A row written before migration 000038 has these NULL, so they read
	// through a nullable string.
	TypeDefinitionID sql.NullString `db:"type_definition_id"`
	EntityID         sql.NullString `db:"entity_id"`
}

// envelopeFrom rebuilds the wire envelope from a stored row.
func envelopeFrom(r outboxRow) events.Envelope {
	return events.Envelope{
		ID:               r.ID.String(),
		Type:             events.Type(r.EventType),
		AggregateType:    r.AggregateType,
		AggregateID:      r.AggregateID,
		TenantID:         r.TenantID,
		TypeDefinitionID: r.TypeDefinitionID.String,
		EntityID:         r.EntityID.String,
		Actor:            r.Actor,
		OccurredAt:       r.OccurredAt,
		RecordedAt:       r.RecordedAt,
		SchemaVersion:    events.SchemaVersion,
		Payload:          json.RawMessage(r.Payload),
	}
}

// Claim leases a batch of pending envelopes to relayID and returns them.
// The lease (claimed_by/claimed_at) reserves the rows so a concurrent relay
// skips them while this relay dispatches outside any transaction; a lease
// older than leaseTTL is reclaimed (its holder is presumed crashed). No
// sequencer lock is taken and no network I/O happens here.
func (s *outboxStore) Claim(ctx context.Context, relayID string, limit int, leaseTTL time.Duration) ([]events.Envelope, error) {
	var rows []outboxRow
	// A single UPDATE ... RETURNING atomically leases the batch; the inner
	// SELECT ... FOR UPDATE SKIP LOCKED keeps two relays off the same rows.
	// next_attempt_at keeps a failing row out of the way until its backoff
	// elapses, and parked_at takes it out entirely once it passes the attempt
	// cap. Without both, a permanently failing row kept its low id, was
	// re-claimed first on every 2-second pass because claims are id-ordered,
	// and starved every newer envelope — stopping pub/sub, webhooks and the
	// feed together, since all three are stamped only for a fully dispatched
	// envelope.
	query := bind(`UPDATE flexitype_event_outbox
		 SET claimed_by = ?, claimed_at = now()
		 WHERE id IN (
		     SELECT id FROM flexitype_event_outbox
		     WHERE dispatched_at IS NULL
		       AND parked_at IS NULL
		       AND next_attempt_at <= now()
		       AND (claimed_at IS NULL OR claimed_at < now() - make_interval(secs => ?))
		     ORDER BY id
		     LIMIT ?
		     FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, tenant_id, actor, event_type, aggregate_type, aggregate_id,
		           payload::text AS payload, occurred_at, recorded_at,
		           type_definition_id, entity_id`)
	if err := txExecer(s.tx).SelectContext(ctx, &rows, query, relayID, leaseTTL.Seconds(), limit); err != nil {
		return nil, fmt.Errorf("claim outbox rows: %w", err)
	}

	envs := make([]events.Envelope, 0, len(rows))
	for _, row := range rows {
		envs = append(envs, envelopeFrom(row))
	}
	return envs, nil
}

// Finalize records dispatch outcomes for a claimed batch under the
// single-sequencer advisory lock (DB-only, no network I/O). Unlike a claim
// pass it BLOCKS on the lock rather than skipping: the batch has already
// been dispatched, so its outcome must be recorded. It re-reads the still
// -pending rows so a batch that was double-claimed after a lease expiry is
// only expanded once.
func (s *outboxStore) Finalize(ctx context.Context, results []outbox.Result) error {
	if len(results) == 0 {
		return nil
	}
	done := make([]string, 0, len(results))
	failed := make([]string, 0)
	lastErrs := make([]any, 0)
	for _, res := range results {
		if res.Err == nil {
			done = append(done, res.EnvelopeID)
		} else {
			failed = append(failed, res.EnvelopeID)
			lastErrs = append(lastErrs, res.Err.Error())
		}
	}

	return s.tx.InTransaction(ctx, func(tx db.Transactor) error {
		q := txExecer(tx)
		// Serialize feed_seq assignment across relays. Blocking (not
		// try): we already dispatched and must finalize.
		if _, err := q.ExecContext(ctx,
			`SELECT pg_advisory_xact_lock(hashtext('flexitype_outbox_expansion'))`); err != nil {
			return fmt.Errorf("acquire expansion lock: %w", err)
		}

		if len(done) > 0 {
			if err := s.expand(ctx, q, done); err != nil {
				return err
			}
		}
		for i, id := range failed {
			// Count the attempt, schedule the retry, and park the row once it
			// passes the cap. attempts was previously written and never read,
			// so nothing bounded the retries and nothing distinguished an
			// undeliverable envelope from a slow one. Only rows still pending
			// are touched (a crash-race copy may have dispatched).
			//
			// The backoff is the delivery lane's shape: 1s, 4s, 16s, 64s,
			// 256s, then a ceiling. Computed in SQL from the incremented
			// attempt count, so two relays racing on the same row converge.
			if _, err := q.ExecContext(ctx, bind(
				`UPDATE flexitype_event_outbox
				 SET attempts = attempts + 1,
				     last_error = ?,
				     claimed_at = NULL,
				     claimed_by = NULL,
				     next_attempt_at = now() + make_interval(secs =>
				         least(power(4, least(attempts, 10))::float8, ?)),
				     parked_at = CASE WHEN attempts + 1 >= ? THEN now() ELSE NULL END
				 WHERE id = ? AND dispatched_at IS NULL`),
				lastErrs[i], s.retryCeiling.Seconds(), s.maxAttempts, id); err != nil {
				return fmt.Errorf("mark outbox failure: %w", err)
			}
		}
		return nil
	})
}

// expand stamps feed_seq on successful envelopes (claim order) and fans
// out one webhook-delivery row per matching active subscription.
func (s *outboxStore) expand(ctx context.Context, q db.QueryExecer, done []string) error {
	// Re-read the still-pending rows in id order. Filtering on
	// dispatched_at IS NULL makes expansion idempotent: if a lease expired
	// and another relay already dispatched one of these, it is skipped
	// here (no duplicate feed_seq, no duplicate delivery rows).
	type expandRow struct {
		ID        ulid.ID `db:"id"`
		TenantID  string  `db:"tenant_id"`
		EventType string  `db:"event_type"`
	}
	var rows []expandRow
	if err := q.SelectContext(ctx, &rows, bind(
		`SELECT id, tenant_id, event_type
		 FROM flexitype_event_outbox
		 WHERE id = ANY(?) AND dispatched_at IS NULL
		 ORDER BY id
		 FOR UPDATE`), pq.Array(done)); err != nil {
		return fmt.Errorf("reread claimed rows: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID.String()
	}

	seqs := make([]int64, 0, len(rows))
	if err := q.SelectContext(ctx, &seqs, bind(
		`SELECT nextval('flexitype_event_feed_seq') FROM generate_series(1, ?)`), len(rows)); err != nil {
		return fmt.Errorf("allocate feed sequence: %w", err)
	}

	if _, err := q.ExecContext(ctx, bind(
		`UPDATE flexitype_event_outbox o
		 SET feed_seq = v.seq, dispatched_at = now(), attempts = o.attempts + 1
		 FROM (SELECT unnest(?::text[]) AS id, unnest(?::bigint[]) AS seq) v
		 WHERE o.id = v.id`), pq.Array(ids), pq.Array(seqs)); err != nil {
		return fmt.Errorf("stamp feed sequence: %w", err)
	}

	// Only the claimed batch's tenants, and only the three columns matching
	// needs. Loading every active subscription in the database made expansion
	// cost a function of total tenant count rather than of the events being
	// dispatched — onboarding a tenant slowed delivery for every existing one,
	// invisibly to any per-tenant metric — and it ran inside the global
	// expansion lock every other relay replica waits on. It also put every
	// tenant's webhook signing secret on the wire on every pass, for a decision
	// that never reads them.
	tenants := make([]string, 0, len(rows))
	seenTenant := make(map[string]bool, len(rows))
	for _, r := range rows {
		if !seenTenant[r.TenantID] {
			seenTenant[r.TenantID] = true
			tenants = append(tenants, r.TenantID)
		}
	}
	var subs []matchingSubscription
	if err := q.SelectContext(ctx, &subs, bind(
		`SELECT id, tenant_id, event_types
		   FROM flexitype_webhook_subscription
		  WHERE active AND tenant_id = ANY(?)`), pq.Array(tenants)); err != nil {
		return fmt.Errorf("load active subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	byTenant := make(map[string][]matchingSubscription, len(tenants))
	for _, sub := range subs {
		byTenant[sub.TenantID] = append(byTenant[sub.TenantID], sub)
	}

	var valueRows []string
	var args []any
	now := time.Now().UTC()
	for i, r := range rows {
		for _, sub := range byTenant[r.TenantID] {
			if !sub.matches(r.EventType) {
				continue
			}
			valueRows = append(valueRows, "(?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)")
			args = append(args,
				ulid.New(), sub.ID, r.ID.String(), r.TenantID, r.EventType, seqs[i], now, now, now)
		}
	}
	if len(valueRows) == 0 {
		return nil
	}

	if _, err := q.ExecContext(ctx, bind(`INSERT INTO flexitype_webhook_delivery
	   (id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status, next_attempt_at, created_at, updated_at)
	 VALUES `+strings.Join(valueRows, ", ")), args...); err != nil {
		return fmt.Errorf("fan out deliveries: %w", err)
	}
	return nil
}

// Compile-time check: the outbox adapter also serves the parked-envelope
// recovery surface (issue #478). The facade asserts to outbox.OpsStore when
// wiring, so a drift here must fail the build, not the boot.
var _ outbox.OpsStore = (*outboxStore)(nil)

// parkedWhere builds the WHERE clause selecting the filter's parked rows.
// Every predicate keeps the partial parked index applicable: tenant_id and id
// are its key columns and parked_at IS NOT NULL is its predicate.
func parkedWhere(filter outbox.ParkedFilter) ([]string, []any) {
	where := []string{"tenant_id = ?", "parked_at IS NOT NULL"}
	args := []any{filter.TenantID.String()}
	if filter.EventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, filter.EventType)
	}
	if filter.ID != "" {
		where = append(where, "id = ?")
		args = append(args, filter.ID)
	}
	return where, args
}

// ListParked returns one keyset page of the tenant's parked envelopes in id
// order (ULIDs, so oldest first), plus the filtered total when asked for.
func (s *outboxStore) ListParked(ctx context.Context, filter outbox.ParkedFilter, page db.Page) ([]outbox.ParkedEnvelope, int, error) {
	where, args := parkedWhere(filter)
	filterClause := strings.Join(where, " AND ")
	filterArgs := append([]any(nil), args...)

	where, args, err := keysetWhere(where, args, idKeyset, page.Cursor)
	if err != nil {
		return nil, 0, err
	}
	args = append(args, page.FetchLimit())

	var rows []struct {
		ID            ulid.ID   `db:"id"`
		EventType     string    `db:"event_type"`
		AggregateType string    `db:"aggregate_type"`
		AggregateID   string    `db:"aggregate_id"`
		Attempts      int       `db:"attempts"`
		LastError     string    `db:"last_error"`
		RecordedAt    time.Time `db:"recorded_at"`
		ParkedAt      time.Time `db:"parked_at"`
	}
	query := `SELECT id, event_type, aggregate_type, aggregate_id, attempts,
	        COALESCE(last_error, '') AS last_error, recorded_at, parked_at
	 FROM flexitype_event_outbox
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY id
	 LIMIT ?`
	if err := txExecer(s.tx).SelectContext(ctx, &rows, bind(query), args...); err != nil {
		return nil, 0, fmt.Errorf("list parked envelopes: %w", err)
	}

	out := make([]outbox.ParkedEnvelope, 0, len(rows))
	for _, r := range rows {
		out = append(out, outbox.ParkedEnvelope{
			ID:            r.ID.String(),
			EventType:     r.EventType,
			AggregateType: r.AggregateType,
			AggregateID:   r.AggregateID,
			Attempts:      r.Attempts,
			LastError:     r.LastError,
			RecordedAt:    r.RecordedAt,
			ParkedAt:      r.ParkedAt,
		})
	}

	total, err := countIf(ctx, txExecer(s.tx), page.WantTotal, func() (string, []any) {
		return `SELECT count(*) FROM flexitype_event_outbox WHERE ` + filterClause, filterArgs
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count parked envelopes: %w", err)
	}
	return out, total, nil
}

// Redrive returns the filter's parked envelopes to the retry queue inside the
// caller's transaction: the park flag and the lease clear, attempts resets to
// zero (a fresh retry budget, mirroring the dead-letter redrive) and the row
// is due immediately. last_error is kept as evidence until the next outcome
// overwrites it. The dispatched_at IS NULL guard is belt-and-braces: parking
// only ever happens on the undispatched failure path.
//
// No batching: unlike the dead-letter redrive, the parked set is bounded by
// the failure window (25 attempts spanning hours per envelope), not by fanout,
// and each redriven row is one small in-place update.
func (s *outboxStore) Redrive(ctx context.Context, tx db.Transactor, filter outbox.ParkedFilter) (int, error) {
	where, args := parkedWhere(filter)
	res, err := txExecer(tx).ExecContext(ctx, bind(
		`UPDATE flexitype_event_outbox
		 SET parked_at = NULL,
		     attempts = 0,
		     next_attempt_at = now(),
		     claimed_at = NULL,
		     claimed_by = NULL
		 WHERE `+strings.Join(where, " AND ")+` AND dispatched_at IS NULL`), args...)
	if err != nil {
		return 0, fmt.Errorf("redrive parked envelopes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("redrive parked envelopes: %w", err)
	}
	return int(n), nil
}

// matchingSubscription is the projection expansion needs: which subscription,
// for which tenant, and which event types it wants. The subscription's URL and
// signing secrets are deliberately absent — the delivery worker loads those
// when it actually sends.
type matchingSubscription struct {
	ID         ulid.ID        `db:"id"`
	TenantID   string         `db:"tenant_id"`
	EventTypes pq.StringArray `db:"event_types"`
}

// matches reports whether the subscription wants an event type. An empty
// list means every type, matching webhook.Subscription.Matches; the active
// flag is already applied by the query.
func (s matchingSubscription) matches(eventType string) bool {
	if len(s.EventTypes) == 0 {
		return true
	}
	for _, t := range s.EventTypes {
		if t == eventType {
			return true
		}
	}
	return false
}
