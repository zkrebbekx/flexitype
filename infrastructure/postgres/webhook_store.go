package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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

func (s *subscriptionStore) WithTx(tx db.Tx) webhook.SubscriptionStore {
	return &subscriptionStore{q: txExecer(tx)}
}

type subscriptionRow struct {
	ID         ulid.ID        `db:"id"`
	TenantID   string         `db:"tenant_id"`
	Name       string         `db:"name"`
	URL        string         `db:"url"`
	Secret     string         `db:"secret"`
	EventTypes pq.StringArray `db:"event_types"`
	Active     bool           `db:"active"`
	CreatedAt  time.Time      `db:"created_at"`
	UpdatedAt  time.Time      `db:"updated_at"`
}

func (r subscriptionRow) toSubscription() webhook.Subscription {
	return webhook.Subscription{
		ID:         r.ID,
		TenantID:   valueobjects.TenantID(r.TenantID),
		Name:       r.Name,
		URL:        r.URL,
		Secret:     r.Secret,
		EventTypes: []string(r.EventTypes),
		Active:     r.Active,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

// subscriptionCols omits previous_secret. Nothing signs with it — a
// delivery carries one signature, computed with secret — so it is no longer
// read or written. The column stays until the next major version so that a
// rollback to an older binary still finds it.
const subscriptionCols = `id, tenant_id, name, url, secret, event_types, active, created_at, updated_at`

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
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		sub.ID, sub.TenantID.String(), sub.Name, sub.URL, sub.Secret,
		textArray(sub.EventTypes), sub.Active, sub.CreatedAt, sub.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}
	return nil
}

func (s *subscriptionStore) Update(ctx context.Context, sub webhook.Subscription) error {
	_, err := s.q.ExecContext(ctx, bind(`UPDATE flexitype_webhook_subscription
	 SET url = ?, secret = ?, event_types = ?, active = ?, updated_at = ?
	 WHERE tenant_id = ? AND id = ?`),
		sub.URL, sub.Secret, textArray(sub.EventTypes), sub.Active,
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
	//
	// An INACTIVE subscription is skipped. Deactivating one used to stop
	// new fan-out only, so its queued backlog kept being delivered — an
	// operator turning a subscription off during an incident still saw the
	// endpoint called. The backlog RESTS rather than dies: the rows stay
	// pending, so reactivating the subscription resumes them.
	//
	// Nothing owned those rows after that, and this comment used to claim the
	// retention pruner bounded them, which was false — a pending delivery of
	// an inactive subscription satisfied none of the three prunes, so it and
	// its envelope were pinned for ever. The pruner's DeadLetterStranded pass
	// now takes them once the subscription has been off longer than the
	// retention, which puts them on the dead-letter path and keeps redrive
	// available until it collects them.
	var ids []string
	err := s.q.SelectContext(ctx, &ids, bind(`UPDATE flexitype_webhook_delivery t
	 SET status = 'inflight', lease_expires_at = ?, updated_at = ?
	 WHERE t.id IN (
	     SELECT d.id FROM flexitype_webhook_delivery d
	     WHERE d.status = 'pending' AND d.next_attempt_at <= ?
	       AND EXISTS (SELECT 1 FROM flexitype_webhook_subscription s
	                   WHERE s.id = d.subscription_id AND s.active)
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
		// NULL for an envelope written before migration 000038, and for an
		// event that concerns no single entity.
		TypeDefinitionID sql.NullString `db:"type_definition_id"`
		EntityID         sql.NullString `db:"entity_id"`
		URL              string         `db:"url"`
		Secret           string         `db:"secret"`
		LeaseExpiresAt   time.Time      `db:"lease_expires_at"`
	}
	if err := s.q.SelectContext(ctx, &rows, bind(`SELECT
	    d.id, d.subscription_id, d.envelope_id, d.tenant_id, d.event_type, d.feed_seq, d.status,
	    d.attempts, d.next_attempt_at, d.last_error, d.response_code, d.created_at, d.updated_at,
	    o.payload::text AS payload, o.actor, o.aggregate_type, o.aggregate_id, o.occurred_at, o.recorded_at,
	    o.type_definition_id, o.entity_id,
	    s.url, s.secret, d.lease_expires_at
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
				ID:               r.EnvelopeID,
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
			},
			URL:            r.URL,
			Secret:         r.Secret,
			LeaseExpiresAt: r.LeaseExpiresAt,
		})
	}
	return out, nil
}

func (s *deliveryStore) Record(ctx context.Context, now time.Time, outcomes ...webhook.Outcome) (int, error) {
	// Every arm carries the ownership predicate: the row must still be
	// inflight under the exact lease this worker took. Updating by id alone
	// let a worker whose lease had lapsed clobber the state of the worker that
	// took over — including rewinding a delivered row back to pending with a
	// next_attempt_at, which produced a third send of the same envelope.
	const owned = ` AND status = 'inflight' AND lease_expires_at = ?`
	lost := 0
	for _, o := range outcomes {
		var query string
		var args []any
		switch {
		case o.Delivered:
			query = `UPDATE flexitype_webhook_delivery
			 SET status = 'delivered', attempts = attempts + 1, response_code = ?,
			     last_error = '', lease_expires_at = NULL, updated_at = ?
			 WHERE id = ?` + owned
			args = []any{o.ResponseCode, now, o.DeliveryID, o.LeaseExpiresAt}
		case o.Dead:
			query = `UPDATE flexitype_webhook_delivery
			 SET status = 'dead', attempts = attempts + 1, response_code = ?,
			     last_error = ?, lease_expires_at = NULL, updated_at = ?
			 WHERE id = ?` + owned
			args = []any{o.ResponseCode, o.Err, now, o.DeliveryID, o.LeaseExpiresAt}
		default:
			query = `UPDATE flexitype_webhook_delivery
			 SET status = 'pending', attempts = attempts + 1, response_code = ?,
			     last_error = ?, next_attempt_at = ?, lease_expires_at = NULL, updated_at = ?
			 WHERE id = ?` + owned
			args = []any{o.ResponseCode, o.Err, o.NextAttemptAt, now, o.DeliveryID, o.LeaseExpiresAt}
		}
		res, err := s.q.ExecContext(ctx, bind(query), args...)
		if err != nil {
			return lost, fmt.Errorf("record delivery outcome: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			lost++
		}
	}
	return lost, nil
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
	filterClause := where
	filterArgs := append([]any(nil), args...)

	whereParts, args, err := keysetWhere([]string{where}, args, []db.KeysetColumn{{Expr: "id", Desc: true}}, page.Cursor)
	if err != nil {
		return nil, 0, err
	}
	args = append(args, page.FetchLimit())

	var rows []deliveryRow
	query := `SELECT ` + deliveryCols + `
	 FROM flexitype_webhook_delivery
	 WHERE ` + strings.Join(whereParts, " AND ") + `
	 ORDER BY id DESC
	 LIMIT ?`
	if err := s.q.SelectContext(ctx, &rows, bind(query), args...); err != nil {
		return nil, 0, fmt.Errorf("list deliveries: %w", err)
	}

	out := make([]webhook.Delivery, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toDelivery())
	}
	total := 0
	if page.WantTotal {
		if err := s.q.GetContext(ctx, &total, bind(
			`SELECT count(*) FROM flexitype_webhook_delivery WHERE `+filterClause), filterArgs...); err != nil {
			return nil, 0, fmt.Errorf("count deliveries: %w", err)
		}
	}
	return out, total, nil
}

// redeliverBatch bounds one redrive statement, and redeliverRamp spreads the
// revived backlog over a window.
//
// Both exist for the same reason the pruner is batched: the endpoint's own
// documentation gives "thousands" as the target scale, and a single unbounded
// UPDATE over that many rows is one long transaction holding locks on a large
// slice of the delivery table — blocking the worker's FOR UPDATE SKIP LOCKED
// claims and the delivery-stats scrape for its duration.
//
// The ramp is PER SUBSCRIPTION, not per delivery.
//
// A per-row random offset was head-of-line blocking, not smoothing. ClaimDue
// takes a subscription's single lowest-feed_seq pending row, and only if that
// row is due, so the head's offset gates everything behind it: measured over
// 20 revived rows for one subscription, the head drew +4m26s and NOTHING was
// claimable for four and a half minutes even though 19 later deliveries were
// already due. The operator sees a redrive that reported success move zero
// deliveries.
//
// That same serialization — lowest feed_seq, one inflight at a time — is
// already the per-endpoint rate limit, so the spike the ramp was written to
// prevent could not occur. What a ramp can still do is stop MANY recovered
// subscriptions firing in the same instant, so the offset is derived from the
// subscription id: deterministic, identical for every row of one
// subscription, and spread across the window between subscriptions.
const (
	redeliverBatch = 500
	redeliverRamp  = 5 * time.Minute
)

// RedeliverMatching returns every dead delivery matching the filter to
// pending, in bounded batches, and reports how many it moved.
//
// It resets attempts as well as the schedule: a redriven delivery starts a
// fresh retry budget, so an endpoint that was down for a day does not exhaust
// its attempts again on the first failure after recovery.
func (s *deliveryStore) RedeliverMatching(ctx context.Context, filter webhook.DeliveryFilter, now time.Time) (int, error) {
	where := []string{"tenant_id = ?", "status = ?"}
	args := []any{filter.TenantID.String(), webhook.StatusDead}
	if !filter.SubscriptionID.IsZero() {
		where = append(where, "subscription_id = ?")
		args = append(args, filter.SubscriptionID)
	}
	clause := strings.Join(where, " AND ")

	total := 0
	for {
		// One offset per SUBSCRIPTION, from a hash of its id: every row of a
		// subscription becomes due at the same instant, so the lowest
		// feed_seq is due first and the backlog drains, while different
		// subscriptions still spread across the window.
		batchArgs := append([]any{now, int(redeliverRamp.Seconds()), now}, args...)
		batchArgs = append(batchArgs, redeliverBatch)
		res, err := s.q.ExecContext(ctx, bind(`UPDATE flexitype_webhook_delivery
		 SET status = 'pending', attempts = 0,
		     next_attempt_at = ?::timestamptz +
		       (((('x' || substr(md5(subscription_id::text), 1, 8))::bit(32)::bigint & 2147483647)
		         % ?::int) * interval '1 second'),
		     lease_expires_at = NULL, updated_at = ?
		 WHERE id IN (
		     SELECT id FROM flexitype_webhook_delivery
		      WHERE `+clause+`
		      ORDER BY id
		      LIMIT ?)`), batchArgs...)
		if err != nil {
			return total, fmt.Errorf("redeliver dead deliveries: %w", err)
		}
		n, aerr := res.RowsAffected()
		if aerr != nil {
			return total, fmt.Errorf("redeliver dead deliveries: %w", aerr)
		}
		total += int(n)
		if int(n) < redeliverBatch {
			break
		}
		if err := ctx.Err(); err != nil {
			return total, nil
		}
	}
	return total, nil
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

	// The status guard is INSIDE the UPDATE, not only in the read above: a
	// worker can claim the row between the SELECT and the UPDATE, and an
	// unguarded rewind to pending mid-send makes the endpoint receive the
	// payload twice. Zero rows affected means the row moved (or vanished)
	// after the read — report the conflict rather than requeueing.
	// attempts, last_error and response_code reset with the status, matching
	// RedeliverMatching. Leaving attempts at the cap gave the redriven
	// delivery no budget: it died again on its first failure, so the action
	// an operator takes to retry one delivery bought exactly one attempt.
	res, err := s.q.ExecContext(ctx, bind(`UPDATE flexitype_webhook_delivery
	 SET status = 'pending', attempts = 0, last_error = '', response_code = 0,
	     next_attempt_at = ?, lease_expires_at = NULL, updated_at = ?
	 WHERE tenant_id = ? AND id = ? AND status NOT IN ('pending', 'inflight')`),
		now, now, tenant.String(), id)
	if err != nil {
		return fmt.Errorf("redeliver: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("redeliver: %w", err)
	}
	if n == 0 {
		return domainerrors.NewConflict(
			"the delivery was claimed or requeued after it was read; it is already in flight or queued",
			"id", id.String())
	}
	return nil
}
