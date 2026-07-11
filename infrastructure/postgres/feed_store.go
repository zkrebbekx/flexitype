package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/application/feed"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// feedStore reads the expanded event log for pull consumers.
type feedStore struct {
	q db.QueryExecer
}

// NewFeedStore builds the events-feed adapter.
func NewFeedStore(q db.QueryExecer) feed.Store {
	return &feedStore{q: q}
}

func (s *feedStore) List(ctx context.Context, tenant valueobjects.TenantID, after int64, types []string, limit int) ([]feed.Event, error) {
	query := `SELECT feed_seq, id, tenant_id, actor, event_type, aggregate_type, aggregate_id,
	        payload::text AS payload, occurred_at, recorded_at
	 FROM flexitype_event_outbox
	 WHERE tenant_id = ? AND feed_seq > ?`
	args := []any{tenant.String(), after}
	if len(types) > 0 {
		query += ` AND event_type = ANY(?)`
		args = append(args, pq.Array(types))
	}
	query += ` ORDER BY feed_seq LIMIT ?`
	args = append(args, limit)

	var rows []struct {
		outboxRow
		FeedSeq int64 `db:"feed_seq"`
	}
	if err := s.q.SelectContext(ctx, &rows, bind(query), args...); err != nil {
		return nil, fmt.Errorf("list feed events: %w", err)
	}

	out := make([]feed.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, feed.Event{
			Seq: r.FeedSeq,
			Envelope: events.Envelope{
				ID:            r.ID.String(),
				Type:          events.Type(r.EventType),
				AggregateType: r.AggregateType,
				AggregateID:   r.AggregateID,
				TenantID:      r.TenantID,
				Actor:         r.Actor,
				OccurredAt:    r.OccurredAt,
				RecordedAt:    r.RecordedAt,
				SchemaVersion: events.SchemaVersion,
				Payload:       json.RawMessage(r.Payload),
			},
		})
	}
	return out, nil
}

func (s *feedStore) Floor(ctx context.Context, tenant valueobjects.TenantID) (int64, error) {
	var floor int64
	if err := s.q.GetContext(ctx, &floor, bind(
		`SELECT COALESCE(min(feed_seq), 0) FROM flexitype_event_outbox
		 WHERE tenant_id = ? AND feed_seq IS NOT NULL`), tenant.String()); err != nil {
		return 0, fmt.Errorf("feed floor: %w", err)
	}
	return floor, nil
}

func (s *feedStore) Prune(ctx context.Context, cutoff time.Time) (int, error) {
	// Deliveries cascade with their envelope; pending/inflight ones keep
	// the envelope alive until they settle.
	res, err := s.q.ExecContext(ctx, bind(`DELETE FROM flexitype_event_outbox o
	 WHERE o.feed_seq IS NOT NULL AND o.recorded_at < ?
	   AND NOT EXISTS (SELECT 1 FROM flexitype_webhook_delivery d
	                   WHERE d.envelope_id = o.id AND d.status IN ('pending', 'inflight'))`), cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// cursorStore persists named feed cursors.
type cursorStore struct {
	q db.QueryExecer
}

// NewCursorStore builds the feed-cursor adapter.
func NewCursorStore(q db.QueryExecer) feed.CursorStore {
	return &cursorStore{q: q}
}

func (s *cursorStore) Get(ctx context.Context, tenant valueobjects.TenantID, consumer string) (int64, error) {
	var position int64
	err := s.q.GetContext(ctx, &position, bind(
		`SELECT position FROM flexitype_event_cursor WHERE tenant_id = ? AND consumer = ?`),
		tenant.String(), consumer)
	if isNoRows(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get cursor: %w", err)
	}
	return position, nil
}

func (s *cursorStore) Commit(ctx context.Context, tenant valueobjects.TenantID, consumer string, position, expected int64, now time.Time) error {
	if expected == 0 {
		// First commit may create the row; the CAS guard still applies if
		// another replica created it with a different position first.
		res, err := s.q.ExecContext(ctx, bind(`INSERT INTO flexitype_event_cursor
		   (tenant_id, consumer, position, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (tenant_id, consumer) DO UPDATE
		   SET position = EXCLUDED.position, updated_at = EXCLUDED.updated_at
		   WHERE flexitype_event_cursor.position = 0`),
			tenant.String(), consumer, position, now)
		if err != nil {
			return fmt.Errorf("commit cursor: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return feed.ErrCursorConflict
		}
		return nil
	}

	res, err := s.q.ExecContext(ctx, bind(`UPDATE flexitype_event_cursor
	 SET position = ?, updated_at = ?
	 WHERE tenant_id = ? AND consumer = ? AND position = ?`),
		position, now, tenant.String(), consumer, expected)
	if err != nil {
		return fmt.Errorf("commit cursor: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return feed.ErrCursorConflict
	}
	return nil
}
