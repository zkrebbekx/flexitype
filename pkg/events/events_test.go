package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

type testEvent struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

func (e testEvent) EventType() Type         { return "flexitype.test.happened" }
func (e testEvent) AggregateType() string   { return "test" }
func (e testEvent) AggregateID() string     { return e.ID }
func (e testEvent) OccurredWhen() time.Time { return e.At }

func TestDispatcher(t *testing.T) {
	Convey("Given a dispatcher with a recording handler", t, func() {
		d := NewDispatcher()
		var received []Envelope
		d.RegisterFunc("recorder", func(_ context.Context, env Envelope) error {
			received = append(received, env)
			return nil
		})

		occurred := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
		evt := testEvent{ID: "agg-1", Name: "hello", At: occurred}
		meta := Metadata{TenantID: "acme", Actor: "service_account:ci"}

		Convey("When an event is dispatched", func() {
			err := d.Dispatch(context.Background(), meta, evt)

			Convey("Then the handler receives a fully-populated envelope", func() {
				So(err, ShouldBeNil)
				So(received, ShouldHaveLength, 1)
				env := received[0]
				So(env.Type, ShouldEqual, Type("flexitype.test.happened"))
				So(env.AggregateType, ShouldEqual, "test")
				So(env.AggregateID, ShouldEqual, "agg-1")
				So(env.TenantID, ShouldEqual, "acme")
				So(env.Actor, ShouldEqual, "service_account:ci")
				So(env.OccurredAt, ShouldEqual, occurred)
				So(env.SchemaVersion, ShouldEqual, SchemaVersion)
				So(env.ID, ShouldNotBeEmpty)

				var payload testEvent
				So(json.Unmarshal(env.Payload, &payload), ShouldBeNil)
				So(payload.Name, ShouldEqual, "hello")
			})
		})

		Convey("When a handler is registered with an event-type filter", func() {
			var filtered []Envelope
			d.RegisterFunc("filtered", func(_ context.Context, env Envelope) error {
				filtered = append(filtered, env)
				return nil
			}, WithEventTypes("flexitype.other.event"))

			err := d.Dispatch(context.Background(), meta, evt)

			Convey("Then non-matching events are not delivered to it", func() {
				So(err, ShouldBeNil)
				So(filtered, ShouldBeEmpty)
				So(received, ShouldHaveLength, 1)
			})
		})

		Convey("When one handler fails and another panics", func() {
			d.RegisterFunc("failing", func(context.Context, Envelope) error {
				return fmt.Errorf("broker down")
			})
			d.RegisterFunc("panicking", func(context.Context, Envelope) error {
				panic("boom")
			})

			err := d.Dispatch(context.Background(), meta, evt)

			Convey("Then the healthy handler still receives the event and errors are joined", func() {
				So(received, ShouldHaveLength, 1)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "broker down")
				So(err.Error(), ShouldContainSubstring, "panic")
			})
		})
	})
}

type fakePublisher struct {
	topics   []string
	messages [][]byte
}

func (p *fakePublisher) Publish(_ context.Context, topic string, msg []byte) error {
	p.topics = append(p.topics, topic)
	p.messages = append(p.messages, msg)
	return nil
}

func TestPublisherHandler(t *testing.T) {
	Convey("Given a dispatcher with a pub/sub publisher hook", t, func() {
		d := NewDispatcher()
		pub := &fakePublisher{}
		d.Register(NewPublisherHandler("fake-broker", pub, nil))

		Convey("When an event is dispatched", func() {
			err := d.Dispatch(context.Background(), Metadata{TenantID: "acme"},
				testEvent{ID: "agg-9", At: time.Now()})

			Convey("Then the broker receives the envelope JSON on the default topic", func() {
				So(err, ShouldBeNil)
				So(pub.topics, ShouldResemble, []string{"flexitype.test.happened"})

				var env Envelope
				So(json.Unmarshal(pub.messages[0], &env), ShouldBeNil)
				So(env.AggregateID, ShouldEqual, "agg-9")
				So(env.TenantID, ShouldEqual, "acme")
			})
		})
	})
}

func TestWebhookHandler(t *testing.T) {
	Convey("Given a webhook receiver expecting signed deliveries", t, func() {
		type delivery struct {
			body      []byte
			signature string
			timestamp string
			eventType string
		}
		var deliveries []delivery

		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			deliveries = append(deliveries, delivery{
				body:      body,
				signature: r.Header.Get(HeaderSignature),
				timestamp: r.Header.Get(HeaderTimestamp),
				eventType: r.Header.Get(HeaderEventType),
			})
			w.WriteHeader(http.StatusNoContent)
		}))
		defer receiver.Close()

		d := NewDispatcher()
		d.Register(NewWebhookHandler("hook", WebhookConfig{URL: receiver.URL, Secret: "s3cret"}))

		Convey("When an event is dispatched", func() {
			err := d.Dispatch(context.Background(), Metadata{}, testEvent{ID: "agg-2", At: time.Now()})

			Convey("Then the receiver gets a verifiable signed envelope", func() {
				So(err, ShouldBeNil)
				So(deliveries, ShouldHaveLength, 1)
				d0 := deliveries[0]
				So(d0.eventType, ShouldEqual, "flexitype.test.happened")
				So(VerifySignature("s3cret", d0.timestamp, d0.body, d0.signature), ShouldBeTrue)
				So(VerifySignature("wrong", d0.timestamp, d0.body, d0.signature), ShouldBeFalse)
				So(VerifyRequest([]string{"old", "s3cret"}, d0.timestamp, d0.body, d0.signature,
					DefaultSignatureTolerance, time.Now()), ShouldBeTrue)
				So(VerifyRequest([]string{"s3cret"}, d0.timestamp, d0.body, d0.signature,
					DefaultSignatureTolerance, time.Now().Add(time.Hour)), ShouldBeFalse)
			})
		})

		Convey("When the receiver returns a server error", func() {
			failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer failing.Close()

			d2 := NewDispatcher()
			d2.Register(NewWebhookHandler("hook", WebhookConfig{URL: failing.URL}))
			err := d2.Dispatch(context.Background(), Metadata{}, testEvent{ID: "agg-3", At: time.Now()})

			Convey("Then dispatch surfaces the delivery failure", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "status 500")
			})
		})
	})
}
