package gcppubsub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/pkg/events"
)

type fakeResult struct {
	id  string
	err error
}

func (r fakeResult) Get(context.Context) (string, error) { return r.id, r.err }

type fakePublisher struct {
	messages []*pubsub.Message
	err      error
	// paused models the broker's per-ordering-key pause: a failed publish
	// pauses the key, and every later publish on it fails immediately until
	// ResumePublish clears it.
	paused  map[string]bool
	resumed []string
}

func (f *fakePublisher) Publish(_ context.Context, msg *pubsub.Message) result {
	if f.paused[msg.OrderingKey] {
		return fakeResult{err: errors.New("pubsub: publishing paused for ordering key")}
	}
	f.messages = append(f.messages, msg)
	if f.err != nil && msg.OrderingKey != "" {
		if f.paused == nil {
			f.paused = map[string]bool{}
		}
		f.paused[msg.OrderingKey] = true
	}
	return fakeResult{id: "m-1", err: f.err}
}

func (f *fakePublisher) ResumePublish(key string) {
	f.resumed = append(f.resumed, key)
	delete(f.paused, key)
}

func envelope() events.Envelope {
	return events.Envelope{
		ID:            "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Type:          "flexitype.attribute_value.set",
		AggregateType: "attribute_value",
		AggregateID:   "01BX5ZZKBKACTAV9WEVGEMMVRZ",
		TenantID:      "acme",
		Actor:         "service_account:ci",
		OccurredAt:    time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC),
		SchemaVersion: events.SchemaVersion,
		Payload:       json.RawMessage(`{"entity_id":"g-1"}`),
	}
}

func TestHandler(t *testing.T) {
	Convey("Given a Pub/Sub handler over a fake publisher", t, func() {
		fake := &fakePublisher{}
		h := &Handler{name: "gcp-pubsub", pub: fake}
		ctx := context.Background()

		Convey("When an envelope is handled", func() {
			err := h.Handle(ctx, envelope())

			Convey("Then one message publishes with the envelope body and filterable attributes", func() {
				So(err, ShouldBeNil)
				So(fake.messages, ShouldHaveLength, 1)
				msg := fake.messages[0]

				var env events.Envelope
				So(json.Unmarshal(msg.Data, &env), ShouldBeNil)
				So(env.ID, ShouldEqual, "01ARZ3NDEKTSV4RRFFQ69G5FAV")

				So(msg.Attributes["event_type"], ShouldEqual, "flexitype.attribute_value.set")
				So(msg.Attributes["tenant_id"], ShouldEqual, "acme")
				So(msg.Attributes["aggregate_id"], ShouldEqual, "01BX5ZZKBKACTAV9WEVGEMMVRZ")
				So(msg.Attributes["event_id"], ShouldEqual, env.ID)
				So(msg.OrderingKey, ShouldBeEmpty)
			})
		})

		Convey("When per-aggregate ordering is enabled", func() {
			WithOrderingKey(PerAggregate)(h)
			err := h.Handle(ctx, envelope())

			Convey("Then the ordering key is tenant/aggregate scoped", func() {
				So(err, ShouldBeNil)
				So(fake.messages[0].OrderingKey, ShouldEqual,
					"acme/attribute_value/01BX5ZZKBKACTAV9WEVGEMMVRZ")
			})
		})

		Convey("When the broker rejects the publish", func() {
			fake.err = errors.New("deadline exceeded")
			err := h.Handle(ctx, envelope())

			Convey("Then the error surfaces so the outbox retries", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "deadline exceeded")
			})
		})
	})
}

// TestOrderingKeyResumesAfterFailure covers the state a failed publish leaves
// behind.
//
// With ordering enabled, a publish failure PAUSES that ordering key: every
// later publish with the same key fails immediately until ResumePublish is
// called. Nothing called it, so one transient error — a deadline, an
// UNAVAILABLE — permanently stopped every event for that aggregate for the
// life of the process. The outbox then retried the same row on every pass and
// failed instantly each time, and because the relay claims in id order and had
// no backoff, those rows starved every newer envelope. Only a restart cleared
// it, and the same trigger re-armed it.
func TestOrderingKeyResumesAfterFailure(t *testing.T) {
	Convey("Given a handler publishing with a per-aggregate ordering key", t, func() {
		fake := &fakePublisher{}
		h := &Handler{name: "gcp-pubsub", pub: fake, orderingKey: PerAggregate}
		ctx := context.Background()
		env := envelope()
		key := PerAggregate(env)

		Convey("When a publish fails", func() {
			fake.err = errors.New("rpc error: code = Unavailable")
			err := h.Handle(ctx, env)
			So(err, ShouldNotBeNil)

			Convey("Then the ordering key is resumed, so a retry can succeed", func() {
				So(fake.resumed, ShouldResemble, []string{key})
			})

			Convey("Then the outbox retry does publish", func() {
				fake.err = nil
				So(h.Handle(ctx, env), ShouldBeNil)
			})

			Convey("Then a later event for the same aggregate is not stuck behind it", func() {
				fake.err = nil
				next := envelope()
				next.ID = "01ARZ3NDEKTSV4RRFFQ69G5FBW"
				So(h.Handle(ctx, next), ShouldBeNil)
			})
		})

		Convey("When a publish fails without an ordering key", func() {
			plain := &fakePublisher{err: errors.New("boom")}
			unordered := &Handler{name: "gcp-pubsub", pub: plain}
			So(unordered.Handle(ctx, env), ShouldNotBeNil)

			Convey("Then there is no key to resume", func() {
				So(plain.resumed, ShouldBeEmpty)
			})
		})

		Convey("When a publish succeeds", func() {
			So(h.Handle(ctx, env), ShouldBeNil)

			Convey("Then nothing is resumed, because nothing paused", func() {
				So(fake.resumed, ShouldBeEmpty)
			})
		})
	})
}
