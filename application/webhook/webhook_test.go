package webhook

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/internal/safedial"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// --- fakes -------------------------------------------------------------------

type fakeTransactor struct {
	pre, post, rollback []db.Hook
}

func (f *fakeTransactor) GetContext(context.Context, any, string, ...any) error {
	return fmt.Errorf("fake transactor: no SQL")
}
func (f *fakeTransactor) SelectContext(context.Context, any, string, ...any) error {
	return fmt.Errorf("fake transactor: no SQL")
}
func (f *fakeTransactor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, fmt.Errorf("fake transactor: no SQL")
}
func (f *fakeTransactor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("fake transactor: no SQL")
}
func (f *fakeTransactor) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func (f *fakeTransactor) Begin(context.Context) (db.Transactor, error) {
	f.pre, f.post, f.rollback = nil, nil, nil
	return f, nil
}

func (f *fakeTransactor) Commit(ctx context.Context) error {
	for _, h := range f.pre {
		if err := h(ctx); err != nil {
			_ = f.Rollback(ctx)
			return err
		}
	}
	for _, h := range f.post {
		if err := h(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeTransactor) Rollback(ctx context.Context) error {
	for _, h := range f.rollback {
		_ = h(ctx)
	}
	return nil
}

func (f *fakeTransactor) InTransaction(ctx context.Context, fn func(tx db.Transactor) error) error {
	if err := fn(f); err != nil {
		_ = f.Rollback(ctx)
		return err
	}
	return f.Commit(ctx)
}

func (f *fakeTransactor) OnPreCommit(h db.Hook)  { f.pre = append(f.pre, h) }
func (f *fakeTransactor) OnPostCommit(h db.Hook) { f.post = append(f.post, h) }
func (f *fakeTransactor) OnRollback(h db.Hook)   { f.rollback = append(f.rollback, h) }

type fakeSubscriptionStore struct {
	items map[string]Subscription
}

func newFakeSubscriptionStore() *fakeSubscriptionStore {
	return &fakeSubscriptionStore{items: map[string]Subscription{}}
}

func (s *fakeSubscriptionStore) WithTx(db.QueryExecer) SubscriptionStore { return s }

func (s *fakeSubscriptionStore) Get(_ context.Context, tenant valueobjects.TenantID, id ulid.ID) (Subscription, error) {
	sub, ok := s.items[id.String()]
	if !ok || sub.TenantID != tenant {
		return Subscription{}, domainerrors.NewNotFound(EntityName, id.String())
	}
	return sub, nil
}

func (s *fakeSubscriptionStore) GetByName(_ context.Context, tenant valueobjects.TenantID, name string) (Subscription, error) {
	for _, sub := range s.items {
		if sub.TenantID == tenant && sub.Name == name {
			return sub, nil
		}
	}
	return Subscription{}, domainerrors.NewNotFound(EntityName, name)
}

func (s *fakeSubscriptionStore) List(_ context.Context, tenant valueobjects.TenantID) ([]Subscription, error) {
	var out []Subscription
	for _, sub := range s.items {
		if sub.TenantID == tenant {
			out = append(out, sub)
		}
	}
	return out, nil
}

func (s *fakeSubscriptionStore) ListActive(context.Context) ([]Subscription, error) {
	var out []Subscription
	for _, sub := range s.items {
		if sub.Active {
			out = append(out, sub)
		}
	}
	return out, nil
}

func (s *fakeSubscriptionStore) Create(_ context.Context, sub Subscription) error {
	s.items[sub.ID.String()] = sub
	return nil
}

func (s *fakeSubscriptionStore) Update(_ context.Context, sub Subscription) error {
	s.items[sub.ID.String()] = sub
	return nil
}

func (s *fakeSubscriptionStore) Delete(_ context.Context, _ valueobjects.TenantID, id ulid.ID) error {
	delete(s.items, id.String())
	return nil
}

// fakeDeliveryStore queues claimed deliveries and records outcomes.
type fakeDeliveryStore struct {
	mu       sync.Mutex
	due      []ClaimedDelivery
	outcomes []Outcome
	released int
	perCall  int // when >0, cap how many rows one ClaimDue returns (models the per-subscription claim)
}

func (s *fakeDeliveryStore) ClaimDue(_ context.Context, limit int, _ time.Duration, _ time.Time) ([]ClaimedDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perCall > 0 && s.perCall < limit {
		limit = s.perCall
	}
	batch := s.due
	if len(batch) > limit {
		batch = batch[:limit]
	}
	s.due = s.due[len(batch):]
	return batch, nil
}

func (s *fakeDeliveryStore) Record(_ context.Context, _ time.Time, outcomes ...Outcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes = append(s.outcomes, outcomes...)
	return nil
}

func (s *fakeDeliveryStore) ReleaseExpired(context.Context, time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released++
	return 0, nil
}

func (s *fakeDeliveryStore) List(context.Context, DeliveryFilter, db.Page) ([]Delivery, int, error) {
	return nil, 0, nil
}

func (s *fakeDeliveryStore) Redeliver(context.Context, valueobjects.TenantID, ulid.ID, time.Time) error {
	return nil
}

type recordedActivity struct {
	entries []activity.Entry
}

func (l *recordedActivity) Write(_ context.Context, _ db.QueryExecer, entries []activity.Entry) error {
	l.entries = append(l.entries, entries...)
	return nil
}

func (l *recordedActivity) List(context.Context, activity.Filter, db.Page) ([]activity.Entry, int, error) {
	return l.entries, len(l.entries), nil
}

// --- subscription usecases -----------------------------------------------------

func TestSubscriptionInteractor(t *testing.T) {
	Convey("Given the webhook subscription usecases", t, func() {
		store := newFakeSubscriptionStore()
		log := &recordedActivity{}
		unit := uow.New(&fakeTransactor{}, events.NewDispatcher(), log)
		i := NewInteractor(unit, store, &fakeDeliveryStore{}, URLPolicy{AllowPrivate: true})
		ctx := context.Background()

		Convey("When a subscription is created", func() {
			sub, err := i.Create(ctx, CreateInput{
				Name:       "billing",
				URL:        "https://billing.internal/hooks",
				Secret:     "s3cret",
				EventTypes: []string{"flexitype.attribute_value.updated"},
			})

			Convey("Then it is active, persisted and audited without secrets", func() {
				So(err, ShouldBeNil)
				So(sub.Active, ShouldBeTrue)
				So(store.items, ShouldHaveLength, 1)
				So(log.entries, ShouldHaveLength, 1)
				So(log.entries[0].Entity, ShouldEqual, EntityName)
				So(string(log.entries[0].After), ShouldNotContainSubstring, "s3cret")
			})

			Convey("And a duplicate name conflicts", func() {
				_, err := i.Create(ctx, CreateInput{Name: "billing", URL: "https://other.example"})
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})

			Convey("And rotating the secret keeps the previous one", func() {
				newSecret := "n3w"
				updated, err := i.Update(ctx, UpdateInput{ID: sub.ID.String(), RotateSecret: &newSecret})
				So(err, ShouldBeNil)
				So(updated.Secret, ShouldEqual, "n3w")
				So(updated.PreviousSecret, ShouldEqual, "s3cret")
			})

			Convey("And Ensure upserts by name", func() {
				ensured, err := i.Ensure(ctx, CreateInput{
					Name: "billing", URL: "https://billing.internal/v2", Secret: "s3cret",
				})
				So(err, ShouldBeNil)
				So(ensured.ID, ShouldEqual, sub.ID)
				So(ensured.URL, ShouldEqual, "https://billing.internal/v2")

				fresh, err := i.Ensure(ctx, CreateInput{Name: "audit", URL: "https://audit.internal"})
				So(err, ShouldBeNil)
				So(fresh.ID, ShouldNotEqual, sub.ID)
			})
		})

		Convey("When invalid input arrives", func() {
			Convey("Then bad names and URLs are rejected", func() {
				_, err := i.Create(ctx, CreateInput{Name: "Bad Name!", URL: "https://x.example"})
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				_, err = i.Create(ctx, CreateInput{Name: "ok-name", URL: "not-a-url"})
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestSubscriptionURLPolicy(t *testing.T) {
	Convey("Given the default (secure) URL policy", t, func() {
		store := newFakeSubscriptionStore()
		unit := uow.New(&fakeTransactor{}, events.NewDispatcher(), &recordedActivity{})
		i := NewInteractor(unit, store, &fakeDeliveryStore{}, URLPolicy{})
		ctx := context.Background()

		Convey("Then non-https and private-host subscriptions are rejected", func() {
			for _, url := range []string{
				"http://public.example/hook",     // not https
				"https://localhost/hook",         // loopback name
				"https://127.0.0.1/hook",         // loopback IP
				"https://10.0.0.5/hook",          // private
				"https://169.254.169.254/latest", // metadata
				"https://192.168.1.1:8443/hook",  // private w/ port
			} {
				_, err := i.Create(ctx, CreateInput{Name: "hook", URL: url})
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			}
		})

		Convey("Then a public https subscription is accepted", func() {
			_, err := i.Create(ctx, CreateInput{Name: "hook", URL: "https://hooks.example.com/x"})
			So(err, ShouldBeNil)
		})
	})

	Convey("Given the allow-private policy (on-prem)", t, func() {
		store := newFakeSubscriptionStore()
		unit := uow.New(&fakeTransactor{}, events.NewDispatcher(), &recordedActivity{})
		i := NewInteractor(unit, store, &fakeDeliveryStore{}, URLPolicy{AllowPrivate: true})

		Convey("Then private http subscriptions are accepted", func() {
			_, err := i.Create(context.Background(), CreateInput{
				Name: "internal", URL: "http://consumer.internal:8080/hook",
			})
			So(err, ShouldBeNil)
		})
	})
}

func TestSubscriptionMatches(t *testing.T) {
	Convey("Given subscriptions with and without event-type filters", t, func() {
		all := Subscription{Active: true}
		filtered := Subscription{Active: true, EventTypes: []string{"a", "b"}}
		inactive := Subscription{Active: false}

		Convey("Then matching honours filters and active state", func() {
			So(all.Matches("anything"), ShouldBeTrue)
			So(filtered.Matches("a"), ShouldBeTrue)
			So(filtered.Matches("c"), ShouldBeFalse)
			So(inactive.Matches("a"), ShouldBeFalse)
		})
	})
}

// --- worker ---------------------------------------------------------------------

func claimed(url string, attempts int) ClaimedDelivery {
	return ClaimedDelivery{
		Delivery: Delivery{
			ID:         ulid.New(),
			EnvelopeID: ulid.New().String(),
			EventType:  "flexitype.test.happened",
			Attempts:   attempts,
		},
		Envelope: events.Envelope{
			ID:            ulid.New().String(),
			Type:          "flexitype.test.happened",
			TenantID:      "default",
			SchemaVersion: events.SchemaVersion,
			Payload:       []byte(`{"ok":true}`),
		},
		URL:    url,
		Secret: "s3cret",
	}
}

func TestWorker(t *testing.T) {
	Convey("Given a delivery worker and a receiving endpoint", t, func() {
		var mu sync.Mutex
		var received []*http.Request
		var bodies [][]byte
		status := http.StatusNoContent

		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			received = append(received, r)
			bodies = append(bodies, body)
			code := status
			mu.Unlock()
			w.WriteHeader(code)
		}))
		defer receiver.Close()

		store := &fakeDeliveryStore{}
		// The httptest server binds loopback, which the default SSRF-guarded
		// client would block; allow private for the delivery-mechanics tests.
		worker := NewWorker(store, WithMaxAttempts(3),
			WithHTTPClient(safedial.NewClient(safedial.Options{AllowPrivate: true})))
		ctx := context.Background()

		Convey("When a delivery succeeds", func() {
			store.due = []ClaimedDelivery{claimed(receiver.URL, 0)}
			worker.pass(ctx)

			Convey("Then the outcome is delivered and the request is signed", func() {
				So(store.outcomes, ShouldHaveLength, 1)
				So(store.outcomes[0].Delivered, ShouldBeTrue)
				So(store.outcomes[0].ResponseCode, ShouldEqual, http.StatusNoContent)

				mu.Lock()
				defer mu.Unlock()
				So(received, ShouldHaveLength, 1)
				sig := received[0].Header.Get(events.HeaderSignature)
				ts := received[0].Header.Get(events.HeaderTimestamp)
				So(events.VerifySignature("s3cret", ts, bodies[0], sig), ShouldBeTrue)
				So(received[0].Header.Get(events.HeaderDelivery), ShouldNotBeEmpty)
			})
		})

		Convey("When there is a backlog and each claim yields one delivery", func() {
			// Models the real per-subscription claim: ClaimDue returns one row
			// per poll. A single pass must still drain the whole backlog.
			store.perCall = 1
			store.due = []ClaimedDelivery{
				claimed(receiver.URL, 0), claimed(receiver.URL, 1), claimed(receiver.URL, 2),
				claimed(receiver.URL, 3), claimed(receiver.URL, 4),
			}
			worker.pass(ctx)

			Convey("Then the whole backlog drains in one pass", func() {
				So(store.outcomes, ShouldHaveLength, 5)
			})
		})

		Convey("When the endpoint fails", func() {
			status = http.StatusInternalServerError
			store.due = []ClaimedDelivery{claimed(receiver.URL, 0)}
			worker.pass(ctx)

			Convey("Then the delivery is scheduled for retry with backoff", func() {
				So(store.outcomes, ShouldHaveLength, 1)
				So(store.outcomes[0].Delivered, ShouldBeFalse)
				So(store.outcomes[0].Dead, ShouldBeFalse)
				So(store.outcomes[0].NextAttemptAt, ShouldHappenAfter, time.Now())
			})
		})

		Convey("When the attempt cap is reached", func() {
			status = http.StatusInternalServerError
			store.due = []ClaimedDelivery{claimed(receiver.URL, 2)} // attempt 3 of 3
			worker.pass(ctx)

			Convey("Then the delivery dead-letters", func() {
				So(store.outcomes, ShouldHaveLength, 1)
				So(store.outcomes[0].Dead, ShouldBeTrue)
				So(store.outcomes[0].Err, ShouldContainSubstring, "status 500")
			})
		})

		Convey("When a pass runs", func() {
			worker.pass(ctx)

			Convey("Then expired leases are released first", func() {
				So(store.released, ShouldEqual, 1)
			})
		})
	})
}

func TestBackoff(t *testing.T) {
	Convey("Given the backoff schedule", t, func() {
		Convey("Then it grows exponentially with a 15m cap", func() {
			for i, want := range map[int]time.Duration{
				1: time.Second,
				2: 4 * time.Second,
				3: 16 * time.Second,
				9: 15 * time.Minute,
			} {
				got := Backoff(i)
				So(got, ShouldBeGreaterThanOrEqualTo, time.Duration(float64(want)*0.75))
				So(got, ShouldBeLessThanOrEqualTo, time.Duration(float64(want)*1.25))
			}
		})
	})
}
