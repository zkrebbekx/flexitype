package postgres

import (
	"context"
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
}

// NewOutboxStore builds the outbox adapter over the pool transactor.
func NewOutboxStore(tx db.Transactor) outbox.Store {
	return &outboxStore{tx: tx}
}

func (s *outboxStore) Write(ctx context.Context, tx db.QueryExecer, envs []events.Envelope) error {
	if len(envs) == 0 {
		return nil
	}

	const cols = 9
	rows := make([]string, 0, len(envs))
	args := make([]any, 0, len(envs)*cols)
	for _, env := range envs {
		rows = append(rows, "(?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			env.ID, env.TenantID, env.Actor, env.Type.String(), env.AggregateType,
			env.AggregateID, jsonbParam(env.Payload), env.OccurredAt, env.RecordedAt,
		)
	}

	query := bind(`INSERT INTO flexitype_event_outbox
	   (id, tenant_id, actor, event_type, aggregate_type, aggregate_id, payload, occurred_at, recorded_at)
	 VALUES ` + strings.Join(rows, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
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
	query := bind(`UPDATE flexitype_event_outbox
		 SET claimed_by = ?, claimed_at = now()
		 WHERE id IN (
		     SELECT id FROM flexitype_event_outbox
		     WHERE dispatched_at IS NULL
		       AND (claimed_at IS NULL OR claimed_at < now() - make_interval(secs => ?))
		     ORDER BY id
		     LIMIT ?
		     FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, tenant_id, actor, event_type, aggregate_type, aggregate_id,
		           payload::text AS payload, occurred_at, recorded_at`)
	if err := s.tx.SelectContext(ctx, &rows, query, relayID, leaseTTL.Seconds(), limit); err != nil {
		return nil, fmt.Errorf("claim outbox rows: %w", err)
	}

	envs := make([]events.Envelope, 0, len(rows))
	for _, row := range rows {
		envs = append(envs, events.Envelope{
			ID:            row.ID.String(),
			Type:          events.Type(row.EventType),
			AggregateType: row.AggregateType,
			AggregateID:   row.AggregateID,
			TenantID:      row.TenantID,
			Actor:         row.Actor,
			OccurredAt:    row.OccurredAt,
			RecordedAt:    row.RecordedAt,
			SchemaVersion: events.SchemaVersion,
			Payload:       json.RawMessage(row.Payload),
		})
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
		// Serialize feed_seq assignment across relays. Blocking (not
		// try): we already dispatched and must finalize.
		if _, err := tx.ExecContext(ctx,
			`SELECT pg_advisory_xact_lock(hashtext('flexitype_outbox_expansion'))`); err != nil {
			return fmt.Errorf("acquire expansion lock: %w", err)
		}

		if len(done) > 0 {
			if err := s.expand(ctx, tx, done); err != nil {
				return err
			}
		}
		for i, id := range failed {
			// Clear the lease so a later pass retries promptly; only touch
			// rows still pending (a crash-race copy may have dispatched).
			if _, err := tx.ExecContext(ctx, bind(
				`UPDATE flexitype_event_outbox
				 SET attempts = attempts + 1, last_error = ?, claimed_at = NULL, claimed_by = NULL
				 WHERE id = ? AND dispatched_at IS NULL`), lastErrs[i], id); err != nil {
				return fmt.Errorf("mark outbox failure: %w", err)
			}
		}
		return nil
	})
}

// expand stamps feed_seq on successful envelopes (claim order) and fans
// out one webhook-delivery row per matching active subscription.
func (s *outboxStore) expand(ctx context.Context, tx db.Transactor, done []string) error {
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
	if err := tx.SelectContext(ctx, &rows, bind(
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
	if err := tx.SelectContext(ctx, &seqs, bind(
		`SELECT nextval('flexitype_event_feed_seq') FROM generate_series(1, ?)`), len(rows)); err != nil {
		return fmt.Errorf("allocate feed sequence: %w", err)
	}

	if _, err := tx.ExecContext(ctx, bind(
		`UPDATE flexitype_event_outbox o
		 SET feed_seq = v.seq, dispatched_at = now(), attempts = o.attempts + 1
		 FROM (SELECT unnest(?::text[]) AS id, unnest(?::bigint[]) AS seq) v
		 WHERE o.id = v.id`), pq.Array(ids), pq.Array(seqs)); err != nil {
		return fmt.Errorf("stamp feed sequence: %w", err)
	}

	var subs []subscriptionRow
	if err := tx.SelectContext(ctx, &subs, bind(
		`SELECT id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at
		 FROM flexitype_webhook_subscription WHERE active`)); err != nil {
		return fmt.Errorf("load active subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	var valueRows []string
	var args []any
	now := time.Now().UTC()
	for i, r := range rows {
		for _, sr := range subs {
			sub := sr.toSubscription()
			if sub.TenantID.String() != r.TenantID || !sub.Matches(r.EventType) {
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

	if _, err := tx.ExecContext(ctx, bind(`INSERT INTO flexitype_webhook_delivery
	   (id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status, next_attempt_at, created_at, updated_at)
	 VALUES `+strings.Join(valueRows, ", ")), args...); err != nil {
		return fmt.Errorf("fan out deliveries: %w", err)
	}
	return nil
}
