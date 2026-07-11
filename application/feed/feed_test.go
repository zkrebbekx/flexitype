package feed

import (
	"context"
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
	})
}
