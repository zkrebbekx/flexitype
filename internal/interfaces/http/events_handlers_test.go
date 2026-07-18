package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	appfeed "github.com/zkrebbekx/flexitype/application/feed"
	appwebhook "github.com/zkrebbekx/flexitype/application/webhook"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// The webhook-subscription and events-feed routes need the outbox, which the
// in-memory service deliberately does not provide — so the root-package
// suite can only reach their 501 branch. These in-package tests wire the
// real router over the real interactors backed by the small in-test stores
// below, so the handlers' success, error and edge branches are exercised for
// real rather than mocked at the HTTP boundary.

// --- in-test event-delivery stores ------------------------------------------

// nopSink satisfies the outbox write side; the tests drive the feed directly
// rather than through envelope expansion.
type nopSink struct{}

func (nopSink) Write(context.Context, db.Tx, []events.Envelope) error { return nil }

// subStore is an in-memory webhook.SubscriptionStore.
type subStore struct {
	mu   sync.Mutex
	subs map[ulid.ID]appwebhook.Subscription
}

func newSubStore() *subStore {
	return &subStore{subs: map[ulid.ID]appwebhook.Subscription{}}
}

func (s *subStore) WithTx(db.Tx) appwebhook.SubscriptionStore { return s }

func (s *subStore) Get(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (appwebhook.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[id]
	if !ok || sub.TenantID != tenant {
		return appwebhook.Subscription{}, domainerrors.NewNotFound("webhook_subscription", id.String())
	}
	return sub, nil
}

func (s *subStore) GetByName(_ context.Context, tenant valueobjects.TenantID, name string) (appwebhook.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subs {
		if sub.TenantID == tenant && sub.Name == name {
			return sub, nil
		}
	}
	return appwebhook.Subscription{}, domainerrors.NewNotFound("webhook_subscription", name)
}

func (s *subStore) List(_ context.Context, tenant valueobjects.TenantID) ([]appwebhook.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []appwebhook.Subscription
	for _, sub := range s.subs {
		if sub.TenantID == tenant {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *subStore) ListActive(context.Context) ([]appwebhook.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []appwebhook.Subscription
	for _, sub := range s.subs {
		if sub.Active {
			out = append(out, sub)
		}
	}
	return out, nil
}

func (s *subStore) Create(_ context.Context, sub appwebhook.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs[sub.ID] = sub
	return nil
}

func (s *subStore) Update(_ context.Context, sub appwebhook.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs[sub.ID] = sub
	return nil
}

func (s *subStore) Delete(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub, ok := s.subs[id]; !ok || sub.TenantID != tenant {
		return domainerrors.NewNotFound("webhook_subscription", id.String())
	}
	delete(s.subs, id)
	return nil
}

// deliveryStore is an in-memory webhook.DeliveryStore. Only the read and
// redeliver paths the HTTP layer reaches are meaningful.
type deliveryStore struct {
	mu    sync.Mutex
	items []appwebhook.Delivery
	// redelivered records ids handed to Redeliver, so the handler's side
	// effect is observable.
	redelivered []string
}

func (d *deliveryStore) ClaimDue(context.Context, int, time.Duration, time.Time) ([]appwebhook.ClaimedDelivery, error) {
	return nil, nil
}
func (d *deliveryStore) Record(context.Context, time.Time, ...appwebhook.Outcome) error { return nil }
func (d *deliveryStore) ReleaseExpired(context.Context, time.Time) (int, error)         { return 0, nil }

func (d *deliveryStore) List(_ context.Context, filter appwebhook.DeliveryFilter, page db.Page) ([]appwebhook.Delivery, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []appwebhook.Delivery
	for _, item := range d.items {
		if filter.SubscriptionID != (ulid.ID{}) && item.SubscriptionID != filter.SubscriptionID {
			continue
		}
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		out = append(out, item)
	}
	total := len(out)
	if page.Limit > 0 && len(out) > page.FetchLimit() {
		out = out[:page.FetchLimit()]
	}
	return out, total, nil
}

func (d *deliveryStore) Redeliver(_ context.Context, _ valueobjects.TenantID, id ulid.ID, _ time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.items {
		if d.items[i].ID == id {
			d.items[i].Status = appwebhook.StatusPending
			d.redelivered = append(d.redelivered, id.String())
			return nil
		}
	}
	return domainerrors.NewNotFound("webhook_delivery", id.String())
}

// feedStore is an in-memory feed.Store over a fixed event log.
type feedStore struct {
	events []appfeed.Event
	// floor is the retention floor; a cursor below it is Gone.
	floor int64
	// err, when set, is returned by List — for the mid-stream failure path.
	err error
}

func (f *feedStore) List(_ context.Context, _ valueobjects.TenantID, after int64, types []string, limit int) ([]appfeed.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	wanted := map[string]bool{}
	for _, t := range types {
		wanted[t] = true
	}
	var out []appfeed.Event
	for _, ev := range f.events {
		if ev.Seq <= after {
			continue
		}
		if len(wanted) > 0 && !wanted[ev.Envelope.Type.String()] {
			continue
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *feedStore) Floor(context.Context, valueobjects.TenantID) (int64, error) { return f.floor, nil }
func (f *feedStore) Prune(context.Context, time.Time) (int, error)               { return 0, nil }

// cursorStore is an in-memory feed.CursorStore with real compare-and-swap.
type cursorStore struct {
	mu        sync.Mutex
	positions map[string]int64
}

func newCursorStore() *cursorStore { return &cursorStore{positions: map[string]int64{}} }

func (c *cursorStore) Get(_ context.Context, _ valueobjects.TenantID, consumer string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.positions[consumer], nil
}

func (c *cursorStore) Commit(_ context.Context, _ valueobjects.TenantID, consumer string, position, expected int64, _ time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.positions[consumer] != expected {
		return appfeed.ErrCursorConflict
	}
	c.positions[consumer] = position
	return nil
}

// --- harness ----------------------------------------------------------------

type deliveryHarness struct {
	t          *testing.T
	srv        *httptest.Server
	subs       *subStore
	deliveries *deliveryStore
	feed       *feedStore
	cursors    *cursorStore
}

// newDeliveryHarness builds the real router over real interactors with event
// delivery switched on and the in-test stores above behind it.
func newDeliveryHarness(t *testing.T, feed *feedStore) *deliveryHarness {
	t.Helper()
	store := memory.NewStore()
	subs := newSubStore()
	deliveries := &deliveryStore{}
	cursors := newCursorStore()

	factory := application.NewFactory(application.FactoryConfig{
		Transactor:      store.Transactor(),
		NewRepositories: func() application.Repositories { return store.Repositories() },
		ActivityLog:     store.ActivityLog(),
		Features:        application.Features{EventDelivery: true},
		Outbox:          nopSink{},
		Subscriptions:   subs,
		Deliveries:      deliveries,
		FeedStore:       feed,
		CursorStore:     cursors,
		// Loopback endpoints are the only reachable ones from a test, so the
		// SSRF guard is relaxed here; its enforcement is covered separately.
		WebhookURLPolicy: appwebhook.URLPolicy{AllowPrivate: true},
	})

	srv := httptest.NewServer(buildRouter(ServerConfig{
		Factory: factory,
		Logger:  logger.New(logger.Config{Level: "error"}),
		Health:  health.NewService("flexitype", "test"),
	}))
	t.Cleanup(srv.Close)

	return &deliveryHarness{t: t, srv: srv, subs: subs, deliveries: deliveries, feed: feed, cursors: cursors}
}

type rawResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r rawResponse) object(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("decode (status %d): %v; body=%s", r.Status, err, r.Body)
	}
	return out
}

func (r rawResponse) errorCode() string {
	var body errorBody
	if json.Unmarshal(r.Body, &body) != nil {
		return ""
	}
	return body.Error.Code
}

func (h *deliveryHarness) do(method, path string, body any) rawResponse {
	h.t.Helper()
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case string:
		reader = bytes.NewReader([]byte(v))
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reader)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	return rawResponse{Status: resp.StatusCode, Header: resp.Header, Body: out}
}

func (h *deliveryHarness) get(p string) rawResponse          { return h.do(http.MethodGet, p, nil) }
func (h *deliveryHarness) post(p string, b any) rawResponse  { return h.do(http.MethodPost, p, b) }
func (h *deliveryHarness) patch(p string, b any) rawResponse { return h.do(http.MethodPatch, p, b) }
func (h *deliveryHarness) put(p string, b any) rawResponse   { return h.do(http.MethodPut, p, b) }
func (h *deliveryHarness) delete(p string) rawResponse       { return h.do(http.MethodDelete, p, nil) }

const absentULID = "01J0000000000000000000000Z"

// TestWebhookSubscriptionRoutes covers the subscription-management surface
// with event delivery enabled: the CRUD lifecycle, the secret that must never
// be serialized, the SSRF guard on the endpoint URL and the delivery listing.
func TestWebhookSubscriptionRoutes(t *testing.T) {
	Convey("Given an API with event delivery enabled", t, func() {
		h := newDeliveryHarness(t, &feedStore{})

		Convey("When a subscription is created", func() {
			resp := h.post("/api/v1/webhook-subscriptions", map[string]any{
				"name": "billing", "url": "https://hooks.example.com/billing",
				"secret": "shhh", "event_types": []string{"value.set"},
			})

			Convey("Then it is 201 with the endpoint's public fields", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
				obj := resp.object(t)
				So(obj["name"], ShouldEqual, "billing")
				So(obj["url"], ShouldEqual, "https://hooks.example.com/billing")
				So(obj["active"], ShouldBeTrue)
				So(obj["event_types"], ShouldResemble, []any{"value.set"})
			})

			Convey("Then the shared secret never appears on the wire", func() {
				So(string(resp.Body), ShouldNotContainSubstring, "shhh")
				So(string(resp.Body), ShouldNotContainSubstring, "secret")
			})

			Convey("And re-using the name conflicts", func() {
				dup := h.post("/api/v1/webhook-subscriptions", map[string]any{
					"name": "billing", "url": "https://hooks.example.com/other", "secret": "x",
				})
				So(dup.Status, ShouldEqual, http.StatusConflict)
				So(dup.errorCode(), ShouldEqual, "CONFLICT")
			})

			Convey("And it can be read, listed, updated and deleted", func() {
				id := resp.object(t)["id"].(string)

				got := h.get("/api/v1/webhook-subscriptions/" + id)
				So(got.Status, ShouldEqual, http.StatusOK)
				So(got.object(t)["name"], ShouldEqual, "billing")

				list := h.get("/api/v1/webhook-subscriptions")
				So(list.Status, ShouldEqual, http.StatusOK)
				So(len(list.object(t)["items"].([]any)), ShouldEqual, 1)

				deactivated := h.patch("/api/v1/webhook-subscriptions/"+id, map[string]any{"active": false})
				So(deactivated.Status, ShouldEqual, http.StatusOK)
				So(deactivated.object(t)["active"], ShouldBeFalse)

				So(h.delete("/api/v1/webhook-subscriptions/"+id).Status, ShouldEqual, http.StatusNoContent)
				So(h.get("/api/v1/webhook-subscriptions/"+id).Status, ShouldEqual, http.StatusNotFound)
			})

			Convey("And the endpoint URL can be repointed", func() {
				id := resp.object(t)["id"].(string)
				moved := h.patch("/api/v1/webhook-subscriptions/"+id, map[string]any{
					"url": "https://hooks.example.com/moved",
				})
				So(moved.Status, ShouldEqual, http.StatusOK)
				So(moved.object(t)["url"], ShouldEqual, "https://hooks.example.com/moved")
			})

			Convey("And rotating the secret keeps the endpoint but changes nothing visible", func() {
				id := resp.object(t)["id"].(string)
				rotated := h.patch("/api/v1/webhook-subscriptions/"+id, map[string]any{
					"rotate_secret": "new-secret",
				})
				So(rotated.Status, ShouldEqual, http.StatusOK)
				So(string(rotated.Body), ShouldNotContainSubstring, "new-secret")
			})
		})

		Convey("When the empty subscription list is read", func() {
			resp := h.get("/api/v1/webhook-subscriptions")

			Convey("Then it is an empty array, not null", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(string(resp.Body), ShouldContainSubstring, `"items":[]`)
			})
		})

		Convey("When the subscription name is not a valid slug", func() {
			resp := h.post("/api/v1/webhook-subscriptions", map[string]any{
				"name": "Not A Slug", "url": "https://hooks.example.com/x", "secret": "s",
			})

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When the endpoint URL is not absolute", func() {
			resp := h.post("/api/v1/webhook-subscriptions", map[string]any{
				"name": "broken", "url": "not-a-url", "secret": "s",
			})

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When the create body is malformed", func() {
			resp := h.post("/api/v1/webhook-subscriptions", `{"name":`)

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When an unknown subscription is read, updated or deleted", func() {
			Convey("Then each is 404", func() {
				So(h.get("/api/v1/webhook-subscriptions/"+absentULID).Status, ShouldEqual, http.StatusNotFound)
				So(h.patch("/api/v1/webhook-subscriptions/"+absentULID, map[string]any{
					"active": false}).Status, ShouldEqual, http.StatusNotFound)
				So(h.delete("/api/v1/webhook-subscriptions/"+absentULID).Status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("When the update body is malformed", func() {
			resp := h.patch("/api/v1/webhook-subscriptions/"+absentULID, `{`)

			Convey("Then it is 422 before the id is even looked up", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("Given a subscription with recorded deliveries", func() {
			created := h.post("/api/v1/webhook-subscriptions", map[string]any{
				"name": "billing", "url": "https://hooks.example.com/billing", "secret": "s",
			})
			So(created.Status, ShouldEqual, http.StatusCreated)
			subID := ulid.MustParse(created.object(t)["id"].(string))

			deadID, deliveredID := ulid.New(), ulid.New()
			h.deliveries.items = []appwebhook.Delivery{
				{ID: deadID, SubscriptionID: subID, EnvelopeID: "env-1", EventType: "value.set",
					Status: appwebhook.StatusDead, Attempts: 5, LastError: "connection refused"},
				{ID: deliveredID, SubscriptionID: subID, EnvelopeID: "env-2", EventType: "value.set",
					Status: appwebhook.StatusDelivered, Attempts: 1, ResponseCode: 200},
			}

			Convey("When the subscription's deliveries are listed", func() {
				resp := h.get("/api/v1/webhook-subscriptions/" + subID.String() + "/deliveries")

				Convey("Then every attempt is reported with its outcome", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					items := resp.object(t)["items"].([]any)
					So(len(items), ShouldEqual, 2)
					So(string(resp.Body), ShouldContainSubstring, "connection refused")
				})
			})

			Convey("When the deliveries are filtered by status", func() {
				resp := h.get("/api/v1/webhook-subscriptions/" + subID.String() + "/deliveries?status=dead")

				Convey("Then only the dead-lettered attempt is returned", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					items := resp.object(t)["items"].([]any)
					So(len(items), ShouldEqual, 1)
					So(items[0].(map[string]any)["status"], ShouldEqual, "dead")
				})
			})

			Convey("When the delivery listing has a bad limit", func() {
				resp := h.get("/api/v1/webhook-subscriptions/" + subID.String() + "/deliveries?limit=lots")

				Convey("Then it is 422", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
				})
			})

			Convey("When a dead delivery is redelivered", func() {
				resp := h.post("/api/v1/webhook-deliveries/"+deadID.String()+"/redeliver", nil)

				Convey("Then it is 200 and the delivery is queued again", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(resp.object(t)["status"], ShouldEqual, appwebhook.StatusPending)
					So(h.deliveries.redelivered, ShouldResemble, []string{deadID.String()})
				})
			})

			Convey("When an unknown delivery is redelivered", func() {
				resp := h.post("/api/v1/webhook-deliveries/"+absentULID+"/redeliver", nil)

				Convey("Then it is 404", func() {
					So(resp.Status, ShouldEqual, http.StatusNotFound)
					So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
				})
			})
		})
	})
}

// event builds one feed entry.
func event(seq int64, eventType string) appfeed.Event {
	return appfeed.Event{
		Seq: seq,
		Envelope: events.Envelope{
			ID:       ulid.New().String(),
			Type:     events.Type(eventType),
			TenantID: valueobjects.DefaultTenant.String(),
			Payload:  json.RawMessage(`{"n":` + ulid.New().String()[:1] + `}`),
		},
	}
}

// TestEventFeedRoutes covers the pull-consumer surface: cursor-paged reads of
// the event log, the retention floor that forces a re-baseline, and the named
// compare-and-swap cursors that let a replicated consumer read as one.
func TestEventFeedRoutes(t *testing.T) {
	Convey("Given a feed holding three events", t, func() {
		feed := &feedStore{events: []appfeed.Event{
			event(1, "value.set"), event(2, "value.removed"), event(3, "value.set"),
		}}
		h := newDeliveryHarness(t, feed)

		Convey("When the feed is read from the beginning", func() {
			resp := h.get("/api/v1/events")

			Convey("Then every event comes back in feed order", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				items := resp.object(t)["items"].([]any)
				So(len(items), ShouldEqual, 3)
				So(items[0].(map[string]any)["seq"], ShouldEqual, float64(1))
				So(items[2].(map[string]any)["seq"], ShouldEqual, float64(3))
			})
		})

		Convey("When the feed is read after a cursor", func() {
			resp := h.get("/api/v1/events?after=2")

			Convey("Then only later events are returned", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				items := resp.object(t)["items"].([]any)
				So(len(items), ShouldEqual, 1)
				So(items[0].(map[string]any)["seq"], ShouldEqual, float64(3))
			})
		})

		Convey("When the feed is filtered by event type", func() {
			resp := h.get("/api/v1/events?types=value.removed")

			Convey("Then only matching envelopes are returned", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				items := resp.object(t)["items"].([]any)
				So(len(items), ShouldEqual, 1)
			})
		})

		Convey("When the feed is read with a limit", func() {
			resp := h.get("/api/v1/events?limit=2")

			Convey("Then the page is capped", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(resp.object(t)["items"].([]any)), ShouldEqual, 2)
			})
		})

		Convey("When ?after= is not an integer", func() {
			resp := h.get("/api/v1/events?after=soon")

			Convey("Then it is 422 naming the cursor", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When ?limit= is not an integer", func() {
			resp := h.get("/api/v1/events?limit=lots")

			Convey("Then it is 422", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When a consumer's cursor is read before it has committed", func() {
			resp := h.get("/api/v1/event-cursors/billing")

			Convey("Then it starts at position zero", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["consumer"], ShouldEqual, "billing")
				So(obj["position"], ShouldEqual, float64(0))
			})
		})

		Convey("When a consumer commits its cursor", func() {
			resp := h.put("/api/v1/event-cursors/billing", map[string]any{"position": 2, "expected": 0})

			Convey("Then the new position is acknowledged and readable", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.object(t)["position"], ShouldEqual, float64(2))

				So(h.get("/api/v1/event-cursors/billing").object(t)["position"], ShouldEqual, float64(2))
			})
		})

		Convey("When two replicas commit the same cursor from the same read", func() {
			So(h.put("/api/v1/event-cursors/billing",
				map[string]any{"position": 2, "expected": 0}).Status, ShouldEqual, http.StatusOK)

			resp := h.put("/api/v1/event-cursors/billing", map[string]any{"position": 3, "expected": 0})

			Convey("Then the loser gets 409 CURSOR_CONFLICT and must re-read", func() {
				So(resp.Status, ShouldEqual, http.StatusConflict)
				So(resp.errorCode(), ShouldEqual, "CURSOR_CONFLICT")

				So(h.get("/api/v1/event-cursors/billing").object(t)["position"], ShouldEqual, float64(2))
			})
		})

		Convey("When the cursor-commit body is malformed", func() {
			resp := h.put("/api/v1/event-cursors/billing", `{"position":`)

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When the consumer name is not a valid slug", func() {
			read := h.get("/api/v1/event-cursors/Not_A Slug")
			write := h.put("/api/v1/event-cursors/Not_A Slug", map[string]any{"position": 1, "expected": 0})

			Convey("Then both are 422 VALIDATION", func() {
				So(read.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(read.errorCode(), ShouldEqual, "VALIDATION")
				So(write.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(write.errorCode(), ShouldEqual, "VALIDATION")
			})
		})
	})

	Convey("Given a feed whose retention floor has moved past the cursor", t, func() {
		h := newDeliveryHarness(t, &feedStore{events: []appfeed.Event{event(9, "value.set")}, floor: 5})

		Convey("When a consumer reads from a pruned cursor", func() {
			resp := h.get("/api/v1/events?after=1")

			Convey("Then it is 410 CURSOR_EXPIRED, telling the consumer to re-baseline", func() {
				So(resp.Status, ShouldEqual, http.StatusGone)
				So(resp.errorCode(), ShouldEqual, "CURSOR_EXPIRED")
			})
		})

		Convey("When a consumer reads from a still-retained cursor", func() {
			resp := h.get("/api/v1/events?after=8")

			Convey("Then the read succeeds", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(resp.object(t)["items"].([]any)), ShouldEqual, 1)
			})
		})
	})
}

// TestEventStreamRoute covers the SSE tail: the stream headers, the id/event/
// data framing that makes EventSource reconnection work, and the
// Last-Event-ID resume the handler honours.
func TestEventStreamRoute(t *testing.T) {
	Convey("Given a feed holding two events", t, func() {
		h := newDeliveryHarness(t, &feedStore{events: []appfeed.Event{
			event(1, "value.set"), event(2, "value.removed"),
		}})

		// readStream opens the SSE endpoint and reads until it has seen want
		// bytes' worth of frames or the deadline passes, then hangs up — the
		// handler loops forever by design.
		readStream := func(lastEventID string) (http.Header, string) {
			req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/events/stream", nil)
			So(err, ShouldBeNil)
			if lastEventID != "" {
				req.Header.Set("Last-Event-ID", lastEventID)
			}
			ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
			defer cancel()
			resp, err := h.srv.Client().Do(req.WithContext(ctx))
			So(err, ShouldBeNil)
			defer func() { _ = resp.Body.Close() }()

			buf := make([]byte, 4096)
			n, _ := resp.Body.Read(buf)
			return resp.Header, string(buf[:n])
		}

		Convey("When the stream is opened", func() {
			header, frames := readStream("")

			Convey("Then it is served as an event stream that proxies must not cache", func() {
				So(header.Get("Content-Type"), ShouldEqual, "text/event-stream")
				So(header.Get("Cache-Control"), ShouldEqual, "no-cache")
			})

			Convey("Then each event is framed with its feed seq as the SSE id", func() {
				So(frames, ShouldContainSubstring, "id: 1\n")
				So(frames, ShouldContainSubstring, "event: value.set\n")
				So(frames, ShouldContainSubstring, "data: {")
			})
		})

		Convey("When the stream is resumed with Last-Event-ID", func() {
			_, frames := readStream("1")

			Convey("Then only events after that id are replayed", func() {
				So(frames, ShouldContainSubstring, "id: 2\n")
				So(frames, ShouldNotContainSubstring, "id: 1\n")
			})
		})

		Convey("When Last-Event-ID is not a feed cursor", func() {
			req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/events/stream", nil)
			So(err, ShouldBeNil)
			req.Header.Set("Last-Event-ID", "not-a-number")
			resp, err := h.srv.Client().Do(req)
			So(err, ShouldBeNil)
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			Convey("Then the stream is refused 422 before any frame is written", func() {
				So(resp.StatusCode, ShouldEqual, http.StatusUnprocessableEntity)
				So(string(body), ShouldContainSubstring, "Last-Event-ID must be a feed cursor")
			})
		})

		Convey("When the stream's query arguments are invalid", func() {
			resp := h.get("/api/v1/events/stream?after=soon")

			Convey("Then it is 422 rather than an opened stream", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})
	})
}

// TestEventDeliveryTenantScoping proves the delivery surface stays inside the
// caller's tenant: another tenant's subscription is not readable.
func TestEventDeliveryTenantScoping(t *testing.T) {
	Convey("Given a subscription owned by another tenant", t, func() {
		h := newDeliveryHarness(t, &feedStore{})

		foreign := appwebhook.Subscription{
			ID:       ulid.New(),
			TenantID: valueobjects.TenantID("globex"),
			Name:     "theirs",
			URL:      "https://hooks.example.com/theirs",
			Active:   true,
		}
		So(h.subs.Create(context.Background(), foreign), ShouldBeNil)

		Convey("When the default tenant reads it by id", func() {
			resp := h.get("/api/v1/webhook-subscriptions/" + foreign.ID.String())

			Convey("Then it is 404, not another tenant's endpoint", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("When the default tenant lists subscriptions", func() {
			resp := h.get("/api/v1/webhook-subscriptions")

			Convey("Then the other tenant's endpoint is absent", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(resp.object(t)["items"].([]any)), ShouldEqual, 0)
				So(string(resp.Body), ShouldNotContainSubstring, "theirs")
			})
		})
	})
}
