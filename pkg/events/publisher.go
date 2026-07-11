package events

import (
	"context"
	"encoding/json"
	"fmt"
)

// Publisher is the seam consumers implement to route events into their own
// pub/sub infrastructure (NATS, Kafka, SNS, Google Pub/Sub, ...). The
// message is the JSON-encoded Envelope, so every subscriber sees the same
// format regardless of broker.
type Publisher interface {
	Publish(ctx context.Context, topic string, message []byte) error
}

// TopicFunc maps an envelope to a broker topic. The default uses the event
// type verbatim, e.g. "flexitype.attribute_definition.created".
type TopicFunc func(env Envelope) string

// DefaultTopic routes each event to a topic named after its event type.
func DefaultTopic(env Envelope) string { return env.Type.String() }

// publisherHandler bridges the dispatcher to a consumer Publisher.
type publisherHandler struct {
	name    string
	pub     Publisher
	topicFn TopicFunc
}

// NewPublisherHandler adapts a Publisher into a dispatcher Handler.
// topicFn may be nil, in which case DefaultTopic is used.
func NewPublisherHandler(name string, pub Publisher, topicFn TopicFunc) Handler {
	if topicFn == nil {
		topicFn = DefaultTopic
	}
	return &publisherHandler{name: name, pub: pub, topicFn: topicFn}
}

func (p *publisherHandler) Name() string { return p.name }

func (p *publisherHandler) Handle(ctx context.Context, env Envelope) error {
	msg, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if err := p.pub.Publish(ctx, p.topicFn(env), msg); err != nil {
		return fmt.Errorf("publish %s: %w", env.Type, err)
	}
	return nil
}
