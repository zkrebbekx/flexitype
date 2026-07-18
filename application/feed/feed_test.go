package feed

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// fakeFeedStore serves a fixed ordered log.
type fakeFeedStore struct {
	events []Event
	pruned int
}

func (s *fakeFeedStore) List(_ context.Context, _ valueobjects.TenantID, after int64, types []string, limit int) ([]Event, error) {
	var out []Event
	for _, ev := range s.events {
		if ev.Seq <= after {
			continue
		}
		if len(types) > 0 {
			match := false
			for _, t := range types {
				if ev.Envelope.Type.String() == t {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, ev)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *fakeFeedStore) Floor(context.Context, valueobjects.TenantID) (int64, error) {
	if len(s.events) == 0 {
		return 0, nil
	}
	return s.events[0].Seq, nil
}

func (s *fakeFeedStore) Prune(context.Context, time.Time) (int, error) {
	s.pruned++
	return 0, nil
}

// fakeCursorStore is an in-memory CAS cursor.
type fakeCursorStore struct {
	positions map[string]int64
}

func (s *fakeCursorStore) key(tenant valueobjects.TenantID, consumer string) string {
	return tenant.String() + "/" + consumer
}

func (s *fakeCursorStore) Get(_ context.Context, tenant valueobjects.TenantID, consumer string) (int64, error) {
	return s.positions[s.key(tenant, consumer)], nil
}

func (s *fakeCursorStore) Commit(_ context.Context, tenant valueobjects.TenantID, consumer string, position, expected int64, _ time.Time) error {
	key := s.key(tenant, consumer)
	if s.positions[key] != expected {
		return ErrCursorConflict
	}
	s.positions[key] = position
	return nil
}

func feedEvent(seq int64, eventType string) Event {
	return Event{
		Seq: seq,
		Envelope: events.Envelope{
			ID:   "env-" + string(rune('a'+seq)),
			Type: events.Type(eventType),
		},
	}
}

func TestFeedInteractor(t *testing.T) {
	Convey("Given an event feed with three retained events", t, func() {
		store := &fakeFeedStore{events: []Event{
			feedEvent(5, "type.a"),
			feedEvent(6, "type.b"),
			feedEvent(7, "type.a"),
		}}
		cursors := &fakeCursorStore{positions: map[string]int64{}}
		i := NewInteractor(store, cursors)
		ctx := context.Background()

		Convey("When listing from the start", func() {
			out, err := i.List(ctx, ListInput{})

			Convey("Then every event returns in order with the next cursor", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 3)
				So(out.NextCursor, ShouldEqual, 7)
			})
		})

		Convey("When listing after a cursor with a type filter", func() {
			out, err := i.List(ctx, ListInput{After: 5, Types: []string{"type.a"}})

			Convey("Then only newer matching events return", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(out.Items[0].Seq, ShouldEqual, 7)
			})
		})

		Convey("When the cursor points before the retention floor", func() {
			_, err := i.List(ctx, ListInput{After: 2})

			Convey("Then the consumer is told to re-baseline", func() {
				So(err, ShouldEqual, ErrGone)
			})
		})

		Convey("When an empty page is requested past the end", func() {
			out, err := i.List(ctx, ListInput{After: 7})

			Convey("Then the cursor stays put", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldBeEmpty)
				So(out.NextCursor, ShouldEqual, 7)
			})
		})

		Convey("When two consumer replicas race a cursor commit", func() {
			So(i.CommitCursor(ctx, "svc-a", 5, 0), ShouldBeNil)
			err := i.CommitCursor(ctx, "svc-a", 6, 0) // stale expected

			Convey("Then the loser gets a conflict and the winner's position holds", func() {
				So(err, ShouldEqual, ErrCursorConflict)
				pos, err := i.Cursor(ctx, "svc-a")
				So(err, ShouldBeNil)
				So(pos, ShouldEqual, 5)
			})
		})

		Convey("When cursor input is invalid", func() {
			Convey("Then bad names and regressions are rejected", func() {
				So(domainerrors.IsValidation(i.CommitCursor(ctx, "Bad Name", 1, 0)), ShouldBeTrue)
				So(i.CommitCursor(ctx, "svc-b", 5, 0), ShouldBeNil)
				So(domainerrors.IsValidation(i.CommitCursor(ctx, "svc-b", 3, 5)), ShouldBeTrue)
			})
		})

		Convey("When a negative position or expected is committed", func() {
			negPos := i.CommitCursor(ctx, "svc-a", -1, 0)
			negExp := i.CommitCursor(ctx, "svc-a", 5, -1)

			Convey("Then both are rejected as validation errors", func() {
				So(domainerrors.IsValidation(negPos), ShouldBeTrue)
				So(domainerrors.IsValidation(negExp), ShouldBeTrue)
			})
		})

		Convey("When a negative After is listed", func() {
			out, err := i.List(ctx, ListInput{After: -1})

			Convey("Then it is a validation error and no page is returned", func() {
				So(out, ShouldBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}

func TestFeedCursorNaming(t *testing.T) {
	Convey("Given a feed interactor with a committed consumer cursor", t, func() {
		store := &fakeFeedStore{}
		cursors := &fakeCursorStore{positions: map[string]int64{}}
		i := NewInteractor(store, cursors)
		ctx := context.Background()
		So(i.CommitCursor(ctx, "billing-sync", 12, 0), ShouldBeNil)

		Convey("When the cursor is read by its exact name", func() {
			pos, err := i.Cursor(ctx, "billing-sync")

			Convey("Then the committed position comes back", func() {
				So(err, ShouldBeNil)
				So(pos, ShouldEqual, 12)
			})
		})

		Convey("When an unknown consumer is read", func() {
			pos, err := i.Cursor(ctx, "never-committed")

			Convey("Then it starts at zero without error", func() {
				So(err, ShouldBeNil)
				So(pos, ShouldEqual, 0)
			})
		})

		Convey("When malformed consumer names are read", func() {
			names := []string{"", "A", "x", "Billing", "billing sync", "billing.sync", "1billing", "-billing"}

			Convey("Then every one is rejected as a validation error and yields position 0", func() {
				for _, name := range names {
					pos, err := i.Cursor(ctx, name)
					So(domainerrors.IsValidation(err), ShouldBeTrue)
					So(pos, ShouldEqual, 0)
				}
			})
		})

		Convey("When a name is exactly at and beyond the length bound", func() {
			ok := "a" + strings.Repeat("b", 63)  // 64 chars: allowed
			bad := "a" + strings.Repeat("b", 64) // 65 chars: rejected
			short := "ab"                        // 2 chars: allowed
			_, okErr := i.Cursor(ctx, ok)
			_, badErr := i.Cursor(ctx, bad)
			_, shortErr := i.Cursor(ctx, short)

			Convey("Then the 2-64 character bound is enforced", func() {
				So(okErr, ShouldBeNil)
				So(shortErr, ShouldBeNil)
				So(domainerrors.IsValidation(badErr), ShouldBeTrue)
			})
		})
	})
}

// errFeedStore fails Prune a fixed number of times, recording each cutoff it
// was called with.
type errFeedStore struct {
	mu       sync.Mutex
	cutoffs  []time.Time
	err      error
	floorErr error
	listErr  error
	floor    int64
	// lastLimit records the limit List was called with, so limit clamping is
	// observable.
	lastLimit int
}

func (s *errFeedStore) List(_ context.Context, _ valueobjects.TenantID, _ int64, _ []string, limit int) ([]Event, error) {
	s.mu.Lock()
	s.lastLimit = limit
	s.mu.Unlock()
	return nil, s.listErr
}

func (s *errFeedStore) Floor(context.Context, valueobjects.TenantID) (int64, error) {
	return s.floor, s.floorErr
}

func (s *errFeedStore) Prune(_ context.Context, cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutoffs = append(s.cutoffs, cutoff)
	return 0, s.err
}

func (s *errFeedStore) calls() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.cutoffs...)
}

func TestFeedListStoreErrors(t *testing.T) {
	Convey("Given a feed interactor over a store that can fail", t, func() {
		store := &errFeedStore{}
		i := NewInteractor(store, &fakeCursorStore{positions: map[string]int64{}})
		ctx := context.Background()

		Convey("When the retention floor cannot be read", func() {
			store.floorErr = errors.New("floor unavailable")
			out, err := i.List(ctx, ListInput{After: 9})

			Convey("Then the store error surfaces unwrapped and no page is returned", func() {
				So(out, ShouldBeNil)
				So(errors.Is(err, store.floorErr), ShouldBeTrue)
			})
		})

		Convey("When the event page cannot be read", func() {
			store.listErr = errors.New("log unavailable")
			out, err := i.List(ctx, ListInput{})

			Convey("Then the store error surfaces and no page is returned", func() {
				So(out, ShouldBeNil)
				So(errors.Is(err, store.listErr), ShouldBeTrue)
			})
		})

		Convey("When a cursor sits exactly at the floor boundary", func() {
			store.floor = 10

			Convey("Then floor-1 is still served but anything older is gone", func() {
				_, err := i.List(ctx, ListInput{After: 9})
				So(err, ShouldBeNil)
				_, err = i.List(ctx, ListInput{After: 8})
				So(err, ShouldEqual, ErrGone)
			})
		})

		Convey("When an out-of-range limit is requested", func() {
			_, err := i.List(ctx, ListInput{Limit: 5000})

			Convey("Then the store sees the clamped default of 100", func() {
				So(err, ShouldBeNil)
				So(store.lastLimit, ShouldEqual, 100)
			})
		})

		Convey("When an in-range limit is requested", func() {
			_, err := i.List(ctx, ListInput{Limit: 25})

			Convey("Then the store sees the caller's limit verbatim", func() {
				So(err, ShouldBeNil)
				So(store.lastLimit, ShouldEqual, 25)
			})
		})
	})
}

func TestPruner(t *testing.T) {
	Convey("Given a retention pruner over a store", t, func() {
		store := &errFeedStore{}
		var observed []error
		var observedMu sync.Mutex
		p := NewPruner(store, 48*time.Hour, func(err error) {
			observedMu.Lock()
			defer observedMu.Unlock()
			observed = append(observed, err)
		})
		fixed := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
		p.now = func() time.Time { return fixed }

		Convey("When it is constructed", func() {
			Convey("Then it defaults to an hourly interval", func() {
				So(p.interval, ShouldEqual, time.Hour)
				So(p.retention, ShouldEqual, 48*time.Hour)
			})
		})

		Convey("When Run is given an already-cancelled context", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			p.Run(ctx)

			Convey("Then it prunes once at now-minus-retention and returns", func() {
				calls := store.calls()
				So(calls, ShouldHaveLength, 1)
				So(calls[0].Equal(fixed.Add(-48*time.Hour)), ShouldBeTrue)
			})
		})

		Convey("When Run ticks repeatedly and the store keeps failing", func() {
			store.err = errors.New("prune failed")
			p.interval = time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { p.Run(ctx); close(done) }()

			deadline := time.After(2 * time.Second)
			for len(store.calls()) < 3 {
				select {
				case <-deadline:
					t.Fatal("pruner did not tick")
				case <-time.After(time.Millisecond):
				}
			}
			cancel()
			<-done

			Convey("Then every failed pass is reported to the error observer", func() {
				observedMu.Lock()
				defer observedMu.Unlock()
				So(len(observed), ShouldBeGreaterThanOrEqualTo, 3)
				So(observed[0].Error(), ShouldEqual, "prune failed")
			})
		})

		Convey("When the pruner has no error observer and the store fails", func() {
			store.err = errors.New("prune failed")
			silent := NewPruner(store, time.Hour, nil)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			Convey("Then Run still returns without panicking", func() {
				So(func() { silent.Run(ctx) }, ShouldNotPanic)
				So(store.calls(), ShouldHaveLength, 1)
			})
		})
	})
}
