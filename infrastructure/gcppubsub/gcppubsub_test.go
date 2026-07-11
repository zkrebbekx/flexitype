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
}

func (f *fakePublisher) Publish(_ context.Context, msg *pubsub.Message) result {
	f.messages = append(f.messages, msg)
	return fakeResult{id: "m-1", err: f.err}
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
