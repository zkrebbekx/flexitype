// Package webhook delivers events to external services over managed
// subscriptions: consumers register an HTTPS endpoint and receive every
// matching envelope as a signed POST, retried with exponential backoff
// and dead-lettered after a cap. One delivery row exists per envelope ×
// subscription, so a dead consumer never affects a healthy one. Design:
// docs/design/event-delivery.md.
package webhook

import (
	"context"
	"net/url"
	"regexp"
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Delivery statuses.
const (
	StatusPending   = "pending"
	StatusInflight  = "inflight"
	StatusDelivered = "delivered"
	StatusDead      = "dead"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

// Subscription is one registered webhook endpoint.
type Subscription struct {
	ID       ulid.ID               `json:"id"`
	TenantID valueobjects.TenantID `json:"tenant_id"`
	Name     string                `json:"name"`
	URL      string                `json:"url"`
	Secret   string                `json:"-"`
	// PreviousSecret stays signing-valid during rotation.
	PreviousSecret string `json:"-"`
	// EventTypes filters deliveries; empty means every event.
	EventTypes []string  `json:"event_types"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Matches reports whether the subscription wants this event type.
func (s Subscription) Matches(eventType string) bool {
	if !s.Active {
		return false
	}
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

// Validate checks the subscription's shape.
func (s Subscription) Validate() error {
	if !namePattern.MatchString(s.Name) {
		return domainerrors.NewValidation("subscription name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return domainerrors.NewValidation("subscription url must be an absolute http(s) URL")
	}
	for _, t := range s.EventTypes {
		if t == "" {
			return domainerrors.NewValidation("event_types must not contain empty entries")
		}
	}
	return nil
}

// SubscriptionStore persists subscriptions. WithTx binds the store to a
// transaction for writes inside a unit of work.
type SubscriptionStore interface {
	WithTx(q db.QueryExecer) SubscriptionStore
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (Subscription, error)
	GetByName(ctx context.Context, tenant valueobjects.TenantID, name string) (Subscription, error)
	List(ctx context.Context, tenant valueobjects.TenantID) ([]Subscription, error)
	// ListActive returns every active subscription across tenants — the
	// expansion step fans envelopes out against this set.
	ListActive(ctx context.Context) ([]Subscription, error)
	Create(ctx context.Context, s Subscription) error
	Update(ctx context.Context, s Subscription) error
	Delete(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error
}

// Delivery is one attempt-tracked (envelope × subscription) pair.
type Delivery struct {
	ID             ulid.ID   `json:"id"`
	SubscriptionID ulid.ID   `json:"subscription_id"`
	EnvelopeID     string    `json:"envelope_id"`
	TenantID       string    `json:"tenant_id"`
	EventType      string    `json:"event_type"`
	FeedSeq        int64     `json:"feed_seq"`
	Status         string    `json:"status"`
	Attempts       int       `json:"attempts"`
	NextAttemptAt  time.Time `json:"next_attempt_at"`
	LastError      string    `json:"last_error,omitempty"`
	ResponseCode   int       `json:"response_code,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ClaimedDelivery is a delivery the worker owns, joined with everything
// needed to POST without further reads.
type ClaimedDelivery struct {
	Delivery
	Envelope events.Envelope
	// Endpoint is the subscription's URL/secret snapshot at claim time.
	URL    string
	Secret string
}

// Outcome records one delivery attempt.
type Outcome struct {
	DeliveryID   ulid.ID
	Delivered    bool
	ResponseCode int
	Err          string
	// NextAttemptAt schedules the retry when not delivered; ignored when
	// Dead is set.
	NextAttemptAt time.Time
	Dead          bool
}

// DeliveryFilter narrows delivery listings.
type DeliveryFilter struct {
	TenantID       valueobjects.TenantID
	SubscriptionID ulid.ID
	Status         string
}

// DeliveryStore persists delivery state.
type DeliveryStore interface {
	// ClaimDue atomically marks up to limit due deliveries inflight (with a
	// lease) and returns them joined with envelope and endpoint. At most
	// one delivery per subscription is claimed, in feed order, and never
	// while another delivery of the same subscription is inflight — this
	// is what keeps per-subscription ordering in the happy path.
	ClaimDue(ctx context.Context, limit int, leaseFor time.Duration, now time.Time) ([]ClaimedDelivery, error)

	// Record persists attempt outcomes.
	Record(ctx context.Context, now time.Time, outcomes ...Outcome) error

	// ReleaseExpired returns inflight deliveries whose lease lapsed (a
	// worker crashed mid-delivery) to pending.
	ReleaseExpired(ctx context.Context, now time.Time) (int, error)

	List(ctx context.Context, filter DeliveryFilter, page db.Page) ([]Delivery, int, error)

	// Redeliver returns a dead (or delivered) delivery to pending now.
	Redeliver(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID, now time.Time) error
}
