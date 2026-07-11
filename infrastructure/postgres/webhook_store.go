package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/application/webhook"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// --- subscriptions ----------------------------------------------------------

type subscriptionStore struct {
	q db.QueryExecer
}

// NewSubscriptionStore builds the webhook-subscription adapter.
func NewSubscriptionStore(q db.QueryExecer) webhook.SubscriptionStore {
	return &subscriptionStore{q: q}
}

func (s *subscriptionStore) WithTx(q db.QueryExecer) webhook.SubscriptionStore {
	return &subscriptionStore{q: q}
}

type subscriptionRow struct {
	ID             ulid.ID        `db:"id"`
	TenantID       string         `db:"tenant_id"`
	Name           string         `db:"name"`
	URL            string         `db:"url"`
	Secret         string         `db:"secret"`
	PreviousSecret string         `db:"previous_secret"`
	EventTypes     pq.StringArray `db:"event_types"`
	Active         bool           `db:"active"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
}

func (r subscriptionRow) toSubscription() webhook.Subscription {
	return webhook.Subscription{
		ID:             r.ID,
		TenantID:       valueobjects.TenantID(r.TenantID),
		Name:           r.Name,
		URL:            r.URL,
		Secret:         r.Secret,
		PreviousSecret: r.PreviousSecret,
		EventTypes:     []string(r.EventTypes),
		Active:         r.Active,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

const subscriptionCols = `id, tenant_id, name, url, secret, previous_secret, event_types, active, created_at, updated_at`

// textArray keeps nil slices as empty SQL arrays (the column is NOT NULL).
func textArray(v []string) pq.StringArray {
	if v == nil {
		return pq.StringArray{}
	}
	return pq.StringArray(v)
}

func (s *subscriptionStore) Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (webhook.Subscription, error) {
	var row subscriptionRow
	err := s.q.GetContext(ctx, &row, bind(`SELECT `+subscriptionCols+`
	 FROM flexitype_webhook_subscription WHERE tenant_id = ? AND id = ?`), tenant.String(), id)
	if isNoRows(err) {
		return webhook.Subscription{}, domainerrors.NewNotFound(webhook.EntityName, id.String())
	}
	if err != nil {
		return webhook.Subscription{}, fmt.Errorf("get subscription: %w", err)
	}
	return row.toSubscription(), nil
}

func (s *subscriptionStore) GetByName(ctx context.Context, tenant valueobjects.TenantID, name string) (webhook.Subscription, error) {
	var row subscriptionRow
	err := s.q.GetContext(ctx, &row, bind(`SELECT `+subscriptionCols+`
	 FROM flexitype_webhook_subscription WHERE tenant_id = ? AND name = ?`), tenant.String(), name)
	if isNoRows(err) {
		return webhook.Subscription{}, domainerrors.NewNotFound(webhook.EntityName, name)
	}
	if err != nil {
		return webhook.Subscription{}, fmt.Errorf("get subscription by name: %w", err)
	}
	return row.toSubscription(), nil
}

func (s *subscriptionStore) List(ctx context.Context, tenant valueobjects.TenantID) ([]webhook.Subscription, error) {
	var rows []subscriptionRow
	if err := s.q.SelectContext(ctx, &rows, bind(`SELECT `+subscriptionCols+`
	 FROM flexitype_webhook_subscription WHERE tenant_id = ? ORDER BY name`), tenant.String()); err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	out := make([]webhook.Subscription, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toSubscription())
	}
	return out, nil
}

func (s *subscriptionStore) ListActive(ctx context.Context) ([]webhook.Subscription, error) {
	var rows []subscriptionRow
	if err := s.q.SelectContext(ctx, &rows, bind(`SELECT `+subscriptionCols+`
	 FROM flexitype_webhook_subscription WHERE active ORDER BY id`)); err != nil {
		return nil, fmt.Errorf("list active subscriptions: %w", err)
	}
	out := make([]webhook.Subscription, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toSubscription())
	}
	return out, nil
}

func (s *subscriptionStore) Create(ctx context.Context, sub webhook.Subscription) error {
	_, err := s.q.ExecContext(ctx, bind(`INSERT INTO flexitype_webhook_subscription
	   (`+subscriptionCols+`)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		sub.ID, sub.TenantID.String(), sub.Name, sub.URL, sub.Secret, sub.PreviousSecret,
		textArray(sub.EventTypes), sub.Active, sub.CreatedAt, sub.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}
	return nil
}

func (s *subscriptionStore) Update(ctx context.Context, sub webhook.Subscription) error {
	_, err := s.q.ExecContext(ctx, bind(`UPDATE flexitype_webhook_subscription
	 SET url = ?, secret = ?, previous_secret = ?, event_types = ?, active = ?, updated_at = ?
	 WHERE tenant_id = ? AND id = ?`),
		sub.URL, sub.Secret, sub.PreviousSecret, textArray(sub.EventTypes), sub.Active,
		sub.UpdatedAt, sub.TenantID.String(), sub.ID)
	if err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}
	return nil
}

func (s *subscriptionStore) Delete(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	if _, err := s.q.ExecContext(ctx, bind(
		`DELETE FROM flexitype_webhook_subscription WHERE tenant_id = ? AND id = ?`),
		tenant.String(), id); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	return nil
}

// --- deliveries -------------------------------------------------------------

type deliveryStore struct {
	q db.QueryExecer
}

// NewDeliveryStore builds the webhook-delivery adapter.
func NewDeliveryStore(q db.QueryExecer) webhook.DeliveryStore {
	return &deliveryStore{q: q}
}

type deliveryRow struct {
	ID             ulid.ID   `db:"id"`
	SubscriptionID ulid.ID   `db:"subscription_id"`
	EnvelopeID     string    `db:"envelope_id"`
	TenantID       string    `db:"tenant_id"`
	EventType      string    `db:"event_type"`
	FeedSeq        int64     `db:"feed_seq"`
	Status         string    `db:"status"`
	Attempts       int       `db:"attempts"`
	NextAttemptAt  time.Time `db:"next_attempt_at"`
	LastError      string    `db:"last_error"`
	ResponseCode   int       `db:"response_code"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}

func (r deliveryRow) toDelivery() webhook.Delivery {
	return webhook.Delivery{
		ID:             r.ID,
		SubscriptionID: r.SubscriptionID,
		EnvelopeID:     r.EnvelopeID,
		TenantID:       r.TenantID,
		EventType:      r.EventType,
		FeedSeq:        r.FeedSeq,
		Status:         r.Status,
		Attempts:       r.Attempts,
		NextAttemptAt:  r.NextAttemptAt,
		LastError:      r.LastError,
		ResponseCode:   r.ResponseCode,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

const deliveryCols = `id, subscription_id, envelope_id, tenant_id, event_type, feed_seq, status,
	attempts, next_attempt_at, last_error, response_code, created_at, updated_at`

func (s *deliveryStore) ClaimDue(ctx context.Context, limit int, leaseFor time.Duration, now time.Time) ([]webhook.ClaimedDelivery, error) {
	// One delivery per subscription (its oldest pending), never while the
	// subscription has an inflight delivery — per-subscription order in
	// the happy path. SKIP LOCKED keeps concurrent workers apart.
	var ids []string
	err := s.q.SelectContext(ctx, &ids, bind(`UPDATE flexitype_webhook_delivery t
	 SET status = 'inflight', lease_expires_at = ?, updated_at = ?
	 WHERE t.id IN (
	     SELECT d.id FROM flexitype_webhook_delivery d
	     WHERE d.status = 'pending' AND d.next_attempt_at <= ?
	       AND NOT EXISTS (SELECT 1 FROM flexitype_webhook_delivery i
	                       WHERE i.subscription_id = d.subscription_id AND i.status = 'inflight')
	       AND d.feed_seq = (SELECT min(d2.feed_seq) FROM flexitype_webhook_delivery d2
	                         WHERE d2.subscription_id = d.subscription_id AND d2.status = 'pending')
	     ORDER BY d.feed_seq
	     LIMIT ?
	     FOR UPDATE SKIP LOCKED
	 )
	 RETURNING t.id`), now.Add(leaseFor), now, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due deliveries: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	var rows []struct {
		deliveryRow
		Payload       string    `db:"payload"`
		Actor         string    `db:"actor"`
		AggregateType string    `db:"aggregate_type"`
		AggregateID   string    `db:"aggregate_id"`
		OccurredAt    time.Time `db:"occurred_at"`
		RecordedAt    time.Time `db:"recorded_at"`
		URL           string    `db:"url"`
		Secret        string    `db:"secret"`
	}
	if err := s.q.SelectContext(ctx, &rows, bind(`SELECT
	    d.id, d.subscription_id, d.envelope_id, d.tenant_id, d.event_type, d.feed_seq, d.status,
	    d.attempts, d.next_attempt_at, d.last_error, d.response_code, d.created_at, d.updated_at,
	    o.payload::text AS payload, o.actor, o.aggregate_type, o.aggregate_id, o.occurred_at, o.recorded_at,
	    s.url, s.secret
	 FROM flexitype_webhook_delivery d
	 JOIN flexitype_event_outbox o ON o.id = d.envelope_id
	 JOIN flexitype_webhook_subscription s ON s.id = d.subscription_id
	 WHERE d.id = ANY(?)
	 ORDER BY d.feed_seq`), pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("load claimed deliveries: %w", err)
	}

	out := make([]webhook.ClaimedDelivery, 0, len(rows))
	for _, r := range rows {
		out = append(out, webhook.ClaimedDelivery{
			Delivery: r.toDelivery(),
			Envelope: events.Envelope{
				ID:            r.EnvelopeID,
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
			URL:    r.URL,
			Secret: r.Secret,
		})
	}
	return out, nil
}

func (s *deliveryStore) Record(ctx context.Context, now time.Time, outcomes ...webhook.Outcome) error {
	for _, o := range outcomes {
		var query string
		var args []any
		switch {
		case o.Delivered:
			query = `UPDATE flexitype_webhook_delivery
			 SET status = 'delivered', attempts = attempts + 1, response_code = ?,
			     last_error = '', lease_expires_at = NULL, updated_at = ?
			 WHERE id = ?`
			args = []any{o.ResponseCode, now, o.DeliveryID}
		case o.Dead:
			query = `UPDATE flexitype_webhook_delivery
			 SET status = 'dead', attempts = attempts + 1, response_code = ?,
			     last_error = ?, lease_expires_at = NULL, updated_at = ?
			 WHERE id = ?`
			args = []any{o.ResponseCode, o.Err, now, o.DeliveryID}
		default:
			query = `UPDATE flexitype_webhook_delivery
			 SET status = 'pending', attempts = attempts + 1, response_code = ?,
			     last_error = ?, next_attempt_at = ?, lease_expires_at = NULL, updated_at = ?
			 WHERE id = ?`
			args = []any{o.ResponseCode, o.Err, o.NextAttemptAt, now, o.DeliveryID}
		}
		if _, err := s.q.ExecContext(ctx, bind(query), args...); err != nil {
			return fmt.Errorf("record delivery outcome: %w", err)
		}
	}
	return nil
}

func (s *deliveryStore) ReleaseExpired(ctx context.Context, now time.Time) (int, error) {
	res, err := s.q.ExecContext(ctx, bind(`UPDATE flexitype_webhook_delivery
	 SET status = 'pending', lease_expires_at = NULL, next_attempt_at = ?, updated_at = ?
	 WHERE status = 'inflight' AND lease_expires_at < ?`), now, now, now)
	if err != nil {
		return 0, fmt.Errorf("release expired leases: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *deliveryStore) List(ctx context.Context, filter webhook.DeliveryFilter, page db.Page) ([]webhook.Delivery, int, error) {
	where := "tenant_id = ?"
	args := []any{filter.TenantID.String()}
	if !filter.SubscriptionID.IsZero() {
		where += " AND subscription_id = ?"
		args = append(args, filter.SubscriptionID)
	}
	if filter.Status != "" {
		where += " AND status = ?"
		args = append(args, filter.Status)
	}
	args = append(args, page.Limit, page.Offset)

	var rows []struct {
		deliveryRow
		TotalCount int `db:"total_count"`
	}
	query := `SELECT ` + deliveryCols + `, count(*) OVER () AS total_count
	 FROM flexitype_webhook_delivery
	 WHERE ` + where + `
	 ORDER BY id DESC
	 LIMIT ? OFFSET ?`
	if err := s.q.SelectContext(ctx, &rows, bind(query), args...); err != nil {
		return nil, 0, fmt.Errorf("list deliveries: %w", err)
	}

	out := make([]webhook.Delivery, 0, len(rows))
	total := 0
	for _, r := range rows {
		out = append(out, r.toDelivery())
		total = r.TotalCount
	}
	return out, total, nil
}

func (s *deliveryStore) Redeliver(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID, now time.Time) error {
	var status string
	err := s.q.GetContext(ctx, &status, bind(
		`SELECT status FROM flexitype_webhook_delivery WHERE tenant_id = ? AND id = ?`),
		tenant.String(), id)
	if isNoRows(err) {
		return domainerrors.NewNotFound("webhook_delivery", id.String())
	}
	if err != nil {
		return fmt.Errorf("load delivery: %w", err)
	}
	if status == webhook.StatusPending || status == webhook.StatusInflight {
		return domainerrors.NewConflict("delivery is already queued", "status", status)
	}

	if _, err := s.q.ExecContext(ctx, bind(`UPDATE flexitype_webhook_delivery
	 SET status = 'pending', next_attempt_at = ?, lease_expires_at = NULL, updated_at = ?
	 WHERE tenant_id = ? AND id = ?`), now, now, tenant.String(), id); err != nil {
		return fmt.Errorf("redeliver: %w", err)
	}
	return nil
}
