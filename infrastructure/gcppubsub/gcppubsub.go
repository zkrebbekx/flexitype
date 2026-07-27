// Package gcppubsub routes flexitype events into Google Cloud Pub/Sub: a
// dispatcher handler that publishes each envelope as one message, with
// attributes for server-side subscription filtering and an optional
// ordering key. Pair with the outbox for at-least-once publishing;
// subscribers dedupe on the envelope id like every other consumer
// (design: docs/design/event-delivery.md).
package gcppubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"cloud.google.com/go/pubsub/v2"

	"github.com/zkrebbekx/flexitype/pkg/events"
)

// OrderingKeyFunc derives a Pub/Sub ordering key from an envelope.
type OrderingKeyFunc func(env events.Envelope) string

// PerAggregate derives one ordering key per aggregate. The topic's publisher
// must have message ordering enabled.
//
// It scopes BROKER-side ordering of what was published; it is not an
// end-to-end per-aggregate ordering guarantee. The outbox relay claims in id
// order but retries a failed row later, and several relay replicas claim
// concurrently, so two envelopes for one aggregate can reach Publish out of
// order. What the key buys is that messages published under it are delivered
// to subscribers in the order the broker accepted them.
func PerAggregate(env events.Envelope) string {
	return env.TenantID + "/" + env.AggregateType + "/" + env.AggregateID
}

// publisher is the seam over *pubsub.Publisher for tests.
type publisher interface {
	Publish(ctx context.Context, msg *pubsub.Message) result
	// ResumePublish clears the paused state a failed publish leaves on an
	// ordering key. Without it the key never recovers inside the process.
	ResumePublish(orderingKey string)
}

type result interface {
	Get(ctx context.Context) (string, error)
}

type gcpPublisher struct{ p *pubsub.Publisher }

func (g gcpPublisher) Publish(ctx context.Context, msg *pubsub.Message) result {
	return g.p.Publish(ctx, msg)
}

func (g gcpPublisher) ResumePublish(orderingKey string) { g.p.ResumePublish(orderingKey) }

// Handler publishes envelopes to one Pub/Sub topic.
type Handler struct {
	name        string
	pub         publisher
	orderingKey OrderingKeyFunc
}

// Option customises the handler.
type Option func(*Handler)

// WithOrderingKey stamps each message with an ordering key (e.g.
// PerAggregate). The *pubsub.Publisher must have EnableMessageOrdering
// set, or publishes fail.
func WithOrderingKey(fn OrderingKeyFunc) Option {
	return func(h *Handler) { h.orderingKey = fn }
}

// New builds a dispatcher handler over a Pub/Sub publisher. The caller
// owns the client's lifecycle.
func New(name string, pub *pubsub.Publisher, opts ...Option) *Handler {
	h := &Handler{name: name, pub: gcpPublisher{pub}}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Name implements events.Handler.
func (h *Handler) Name() string { return h.name }

// Handle implements events.Handler: one envelope, one message. The
// publish is confirmed synchronously so outbox retries see real failures.
func (h *Handler) Handle(ctx context.Context, env events.Envelope) error {
	msg, err := h.message(env)
	if err != nil {
		return err
	}
	if _, err := h.pub.Publish(ctx, msg).Get(ctx); err != nil {
		// A failed publish on an ordering key PAUSES that key: every later
		// Publish with the same key fails immediately until it is resumed.
		// Nothing resumed it, so one transient error — a deadline, an
		// UNAVAILABLE — permanently stopped every event for that aggregate for
		// the life of the process, and the outbox retried the same row on
		// every pass while failing instantly. Resuming here lets the retry do
		// its job; the error still propagates so the relay reschedules.
		if msg.OrderingKey != "" {
			h.pub.ResumePublish(msg.OrderingKey)
		}
		return fmt.Errorf("publish %s to pub/sub: %w", env.Type, err)
	}
	return nil
}

// message renders the envelope: JSON body plus attributes so subscribers
// can filter server-side (attributes.event_type = "..." etc.) without
// decoding payloads.
func (h *Handler) message(env events.Envelope) (*pubsub.Message, error) {
	data, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	msg := &pubsub.Message{
		Data: data,
		Attributes: map[string]string{
			"event_id":       env.ID,
			"event_type":     env.Type.String(),
			"aggregate_type": env.AggregateType,
			"aggregate_id":   env.AggregateID,
			"tenant_id":      env.TenantID,
			"actor":          env.Actor,
			"schema_version": strconv.Itoa(env.SchemaVersion),
			"occurred_at":    env.OccurredAt.UTC().Format(time.RFC3339Nano),
		},
	}
	if h.orderingKey != nil {
		msg.OrderingKey = h.orderingKey(env)
	}
	return msg, nil
}
