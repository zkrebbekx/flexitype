// Package webhook delivers events to external services over managed
// subscriptions: consumers register an HTTPS endpoint and receive every
// matching envelope as a signed POST, retried with exponential backoff
// and dead-lettered after a cap. One delivery row exists per envelope ×
// subscription, so a dead consumer never affects a healthy one. Design:
// docs/design/event-delivery.md.
package webhook

import (
	"context"
	"net"
	"net/url"
	"regexp"
	"strings"
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

// URLPolicy governs which subscription URLs are accepted. The zero value
// is the safe default: https only, no private/loopback/link-local hosts.
type URLPolicy struct {
	// AllowPrivate permits http and private/loopback/link-local hosts —
	// for on-prem deployments whose consumers live on internal networks.
	// The delivery worker's dialer guard must be relaxed in step.
	AllowPrivate bool
}

// Validate checks the subscription's shape and its URL against the policy.
func (s Subscription) Validate(policy URLPolicy) error {
	if !namePattern.MatchString(s.Name) {
		return domainerrors.NewValidation("subscription name must be lowercase alphanumeric with _ or -, 2-64 chars")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" {
		return domainerrors.NewValidation("subscription url must be an absolute URL")
	}
	if policy.AllowPrivate {
		if u.Scheme != "https" && u.Scheme != "http" {
			return domainerrors.NewValidation("subscription url must be http or https")
		}
	} else {
		if u.Scheme != "https" {
			return domainerrors.NewValidation("subscription url must be https")
		}
		if host := hostOnly(u.Host); isLiteralPrivateHost(host) {
			return domainerrors.NewValidation(
				"subscription url must target a public host; private, loopback and link-local addresses are not allowed",
				"host", host)
		}
	}
	for _, t := range s.EventTypes {
		if t == "" {
			return domainerrors.NewValidation("event_types must not contain empty entries")
		}
	}
	return nil
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// isLiteralPrivateHost catches obvious non-public targets at write time.
// It is a fast-fail UX guard; the delivery worker's dialer is the
// authoritative check (it resolves names and defeats DNS rebinding).
func isLiteralPrivateHost(host string) bool {
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // a name — resolution-time guard handles it
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
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
