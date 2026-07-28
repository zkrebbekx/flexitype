package changeset

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// TestPublishFailureObserver covers the report a scheduled publish makes when
// it fails.
//
// PublishDue discarded each set's error with a bare continue, so a change-set
// that could never publish retried for ever with no log line, no metric and
// no observer callback. The only symptom was a scheduled change that never
// arrived, and the observer #134 introduced for exactly this class of failure
// never saw it.
func TestPublishFailureObserver(t *testing.T) {
	Convey("Given an interactor with no observer wired", t, func() {
		i := &Interactor{}

		Convey("Then reporting a failure is a no-op rather than a panic", func() {
			So(func() {
				i.reportPublishFailure(ChangeSet{ID: ulid.New()}, errors.New("boom"))
			}, ShouldNotPanic)
		})
	})

	Convey("Given an interactor with an observer", t, func() {
		var seen []string
		var seenErr error
		i := &Interactor{}
		i.OnPublishFailure(func(cs ChangeSet, err error) {
			seen = append(seen, cs.Name)
			seenErr = err
		})

		Convey("When a set fails to publish", func() {
			boom := errors.New("attribute was archived")
			i.reportPublishFailure(ChangeSet{
				ID: ulid.New(), Name: "spring pricing", TenantID: valueobjects.DefaultTenant,
			}, boom)

			Convey("Then the observer sees the set and the cause", func() {
				So(seen, ShouldResemble, []string{"spring pricing"})
				So(errors.Is(seenErr, boom), ShouldBeTrue)
			})
		})
	})

	Convey("Given a scheduler tick with nothing due", t, func() {
		i := &Interactor{store: emptyStore{}, now: func() time.Time { return time.Unix(0, 0).UTC() }}

		Convey("Then it publishes nothing and reports no error", func() {
			n, err := i.PublishDue(context.Background())
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 0)
		})
	})
}

// emptyStore has nothing due.
type emptyStore struct{ Store }

func (emptyStore) DueForPublish(context.Context, time.Time) ([]ChangeSet, error) { return nil, nil }

// claimStore records the sequence of states a publish writes, so the ordering
// between the compare-and-swap and the mutations is observable.
type claimStore struct {
	cs      ChangeSet
	states  []State
	failAt  int
	updates int
}

func (s *claimStore) Create(context.Context, ChangeSet) error { return nil }

func (s *claimStore) Get(_ context.Context, _ valueobjects.TenantID, _ ulid.ID) (ChangeSet, error) {
	return s.cs, nil
}

func (s *claimStore) List(context.Context, valueobjects.TenantID) ([]ChangeSet, error) {
	return []ChangeSet{s.cs}, nil
}

func (s *claimStore) Update(_ context.Context, cs ChangeSet) error {
	s.updates++
	if s.failAt == s.updates {
		return errors.New("the change-set was modified by someone else")
	}
	s.states = append(s.states, cs.State)
	cs.Version++
	s.cs = cs
	return nil
}

func (s *claimStore) DueForPublish(context.Context, time.Time) ([]ChangeSet, error) { return nil, nil }

// StalePublishing serves the reclaim path (changeset.ClaimReclaimer).
func (s *claimStore) StalePublishing(_ context.Context, before time.Time) ([]ChangeSet, error) {
	if s.cs.State != StatePublishing || s.cs.UpdatedAt.After(before) {
		return nil, nil
	}
	return []ChangeSet{s.cs}, nil
}

// applier drives the one call publish makes into the value interactor. err is
// returned as-is; ctxErr records whether the context it was handed was alive.
type applier struct {
	err   error
	calls int
}

func (a *applier) ApplyMutations(context.Context, []appvalue.Mutation) error {
	a.calls++
	return a.err
}

// TestPublishTakesTheClaimFirst pins the ordering that the optimistic-locking
// fix inverted.
//
// Publish applied the mutations and only then compare-and-swapped the record.
// Once that call could fail, a concurrent touch of the set left the data
// committed and the record saying something else — and through PublishDue the
// set stayed approved with publish_at in the past, so every tick re-applied
// the same mutations over whatever had been written in between.
func TestPublishTakesTheClaimFirst(t *testing.T) {
	Convey("Given an approved change-set", t, func() {
		base := ChangeSet{
			ID: ulid.New(), TenantID: valueobjects.DefaultTenant,
			State: StateApproved, Version: 1,
		}

		Convey("When the FIRST compare-and-swap fails", func() {
			store := &claimStore{cs: base, failAt: 1}
			i := &Interactor{store: store, values: &applier{}, now: time.Now}
			err := i.publish(context.Background(), &base)

			Convey("Then it stops before the mutations, so nothing is applied", func() {
				So(err, ShouldNotBeNil)
				So(store.states, ShouldBeEmpty)
			})
		})

		Convey("When the publish runs with no mutations to apply", func() {
			store := &claimStore{cs: base}
			i := &Interactor{store: store, values: &applier{}, now: time.Now}
			err := i.publish(context.Background(), &base)

			Convey("Then it claims first and finalises second", func() {
				So(err, ShouldBeNil)
				So(store.states, ShouldResemble, []State{StatePublishing, StatePublished})
			})
		})
	})
}
