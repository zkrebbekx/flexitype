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

func (s *outboxStore) FetchPending(ctx context.Context, limit int, fn func(envs []events.Envelope) []outbox.Result) error {
	return s.tx.InTransaction(ctx, func(tx db.Transactor) error {
		// SKIP LOCKED lets concurrent relays share the queue without
		// claiming the same rows.
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
			if _, err := tx.ExecContext(ctx, bind(
				`UPDATE flexitype_event_outbox SET dispatched_at = now(), attempts = attempts + 1
				 WHERE id = ANY(?)`), pq.Array(done)); err != nil {
				return fmt.Errorf("mark outbox dispatched: %w", err)
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
