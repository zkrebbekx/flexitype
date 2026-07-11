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

func (s *outboxStore) Expand(ctx context.Context, limit int, fn func(envs []events.Envelope) []outbox.Result) error {
	return s.tx.InTransaction(ctx, func(tx db.Transactor) error {
		// One sequencer at a time: feed_seq must be assigned in commit
		// order or feed cursors would skip rows. Another replica holding
		// the lock means expansion is already running — skip this pass.
		var locked bool
		if err := tx.GetContext(ctx, &locked,
			`SELECT pg_try_advisory_xact_lock(hashtext('flexitype_outbox_expansion'))`); err != nil {
			return fmt.Errorf("acquire expansion lock: %w", err)
		}
		if !locked {
			return nil
		}

		var rows []outboxRow
		query := bind(`SELECT id, tenant_id, actor, event_type, aggregate_type, aggregate_id,
		        payload::text AS payload, occurred_at, recorded_at
		 FROM flexitype_event_outbox
		 WHERE dispatched_at IS NULL
		 ORDER BY id
		 LIMIT ?
		 FOR UPDATE SKIP LOCKED`)
		if err := tx.SelectContext(ctx, &rows, query, limit); err != nil {
			return fmt.Errorf("claim outbox rows: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}

		byID := make(map[string]outboxRow, len(rows))
		envs := make([]events.Envelope, 0, len(rows))
		for _, row := range rows {
			byID[row.ID.String()] = row
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

		var done, failed []string
		var lastErrs []any
		for _, res := range fn(envs) {
			if res.Err == nil {
				done = append(done, res.EnvelopeID)
			} else {
				failed = append(failed, res.EnvelopeID)
				lastErrs = append(lastErrs, res.Err.Error())
			}
		}

		if len(done) > 0 {
			if err := s.expand(ctx, tx, done, byID); err != nil {
				return err
			}
		}
		for i, id := range failed {
			if _, err := tx.ExecContext(ctx, bind(
				`UPDATE flexitype_event_outbox SET attempts = attempts + 1, last_error = ?
				 WHERE id = ?`), lastErrs[i], id); err != nil {
				return fmt.Errorf("mark outbox failure: %w", err)
			}
		}
		return nil
	})
}

// expand stamps feed_seq on successful envelopes (claim order) and fans
// out one webhook-delivery row per matching active subscription.
func (s *outboxStore) expand(ctx context.Context, tx db.Transactor, done []string, byID map[string]outboxRow) error {
	seqs := make([]int64, 0, len(done))
	if err := tx.SelectContext(ctx, &seqs, bind(
		`SELECT nextval('flexitype_event_feed_seq') FROM generate_series(1, ?)`), len(done)); err != nil {
		return fmt.Errorf("allocate feed sequence: %w", err)
	}

	if _, err := tx.ExecContext(ctx, bind(
		`UPDATE flexitype_event_outbox o
		 SET feed_seq = v.seq, dispatched_at = now(), attempts = o.attempts + 1
		 FROM (SELECT unnest(?::text[]) AS id, unnest(?::bigint[]) AS seq) v
		 WHERE o.id = v.id`), pq.Array(done), pq.Array(seqs)); err != nil {
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
	for i, id := range done {
		row := byID[id]
		for _, sr := range subs {
			sub := sr.toSubscription()
			if sub.TenantID.String() != row.TenantID || !sub.Matches(row.EventType) {
				continue
			}
			valueRows = append(valueRows, "(?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)")
			args = append(args,
				ulid.New(), sub.ID, id, row.TenantID, row.EventType, seqs[i], now, now, now)
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
