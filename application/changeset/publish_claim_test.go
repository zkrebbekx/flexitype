package changeset

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// ctxStore fails every write whose context has ended, the way a real store
// does, and records the states it accepted.
type ctxStore struct {
	cs     ChangeSet
	states []State
}

func (s *ctxStore) Create(context.Context, ChangeSet) error { return nil }

func (s *ctxStore) Get(_ context.Context, _ valueobjects.TenantID, _ ulid.ID) (ChangeSet, error) {
	return s.cs, nil
}

func (s *ctxStore) List(context.Context, valueobjects.TenantID) ([]ChangeSet, error) {
	return []ChangeSet{s.cs}, nil
}

func (s *ctxStore) Update(ctx context.Context, cs ChangeSet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.states = append(s.states, cs.State)
	cs.Version++
	s.cs = cs
	return nil
}

func (s *ctxStore) DueForPublish(context.Context, time.Time) ([]ChangeSet, error) { return nil, nil }

func (s *ctxStore) StalePublishing(_ context.Context, before time.Time) ([]ChangeSet, error) {
	if s.cs.State != StatePublishing || s.cs.UpdatedAt.After(before) {
		return nil, nil
	}
	return []ChangeSet{s.cs}, nil
}

// TestPublishReleasesClaimOnADeadContext proves a publish that ends with the
// caller's context still hands the claim back.
//
// Publish claims the set (state publishing) before it applies the mutations,
// and the release used the caller's context. The commonest reason the publish
// fails is that this very context ended — a client timeout, a load-balancer
// idle timeout, a pod eviction — so the release failed too. Reject refuses
// publishing, Publish refused it, AddMutation refuses it and the scheduler
// selected only approved: the set was stranded with no API able to move it,
// and its mutations unapplied.
func TestPublishReleasesClaimOnADeadContext(t *testing.T) {
	Convey("Given an approved change-set whose publish fails with the context", t, func() {
		ctx, cancel := context.WithCancel(context.Background())
		store := &ctxStore{cs: ChangeSet{
			ID: ulid.New(), TenantID: valueobjects.DefaultTenant,
			State: StateApproved, Version: 1,
		}}
		// The applier ends the context, as a request timeout does, and
		// reports the same failure the value interactor would.
		boom := context.Canceled
		i := &Interactor{store: store, now: time.Now, values: applierFunc(func() error {
			cancel()
			return boom
		})}
		cs := store.cs

		Convey("When the set is published", func() {
			err := i.publish(ctx, &cs)

			Convey("Then the claim is released and the set is approved again", func() {
				So(errors.Is(err, boom), ShouldBeTrue)
				So(store.states, ShouldResemble, []State{StatePublishing, StateApproved})
				So(store.cs.State, ShouldEqual, StateApproved)
				So(cs.State, ShouldEqual, StateApproved)
			})
		})
	})
}

// applierFunc adapts a function to the mutation applier.
type applierFunc func() error

func (f applierFunc) ApplyMutations(context.Context, []appvalue.Mutation) error { return f() }

// TestStalePublishClaimIsReclaimable proves a claim left behind by a publish
// that never finished does not strand the set for ever.
func TestStalePublishClaimIsReclaimable(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	base := ChangeSet{
		ID: ulid.New(), TenantID: valueobjects.DefaultTenant,
		State: StatePublishing, Version: 1,
	}

	Convey("Given a set left in publishing", t, func() {
		Convey("When the claim is still fresh", func() {
			store := &ctxStore{cs: base}
			store.cs.UpdatedAt = now.Add(-time.Minute)
			i := &Interactor{store: store, values: &applier{}, now: func() time.Time { return now }}
			cs := store.cs
			err := i.publish(context.Background(), &cs)

			Convey("Then publishing is refused and the message says when it can be retried", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "publishing")
				So(strings.Contains(err.Error(), "stale"), ShouldBeTrue)
				So(store.states, ShouldBeEmpty)
			})

			Convey("Then the scheduler does not reclaim it", func() {
				n, perr := i.PublishDue(context.Background())
				So(perr, ShouldBeNil)
				So(n, ShouldEqual, 0)
			})
		})

		Convey("When the claim is older than the TTL", func() {
			store := &ctxStore{cs: base}
			store.cs.UpdatedAt = now.Add(-PublishClaimTTL - time.Minute)
			i := &Interactor{store: store, values: &applier{}, now: func() time.Time { return now }}

			Convey("Then an explicit publish reclaims it and completes", func() {
				cs := store.cs
				err := i.publish(context.Background(), &cs)
				So(err, ShouldBeNil)
				So(store.states, ShouldResemble, []State{StatePublishing, StatePublished})
			})

			Convey("Then the scheduler reclaims it too", func() {
				n, perr := i.PublishDue(context.Background())
				So(perr, ShouldBeNil)
				So(n, ShouldEqual, 1)
				So(store.cs.State, ShouldEqual, StatePublished)
			})

			Convey("Then a reclaim that fails releases to approved, not to publishing", func() {
				failing := &Interactor{store: store, now: func() time.Time { return now },
					values: &applier{err: errors.New("attribute was archived")}}
				cs := store.cs
				err := failing.publish(context.Background(), &cs)
				So(err, ShouldNotBeNil)
				So(store.cs.State, ShouldEqual, StateApproved)
			})
		})
	})
}

// TestRejectNamesTheReclaimPath proves the refusal to reject a publishing set
// tells the operator what to do instead.
//
// Rejecting stays refused: a stranded publish may have committed its values
// before it failed, and a rejected set would then report untouched data for
// changes that are live.
func TestRejectNamesTheReclaimPath(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	Convey("Given a set stranded in publishing", t, func() {
		store := &ctxStore{cs: ChangeSet{
			ID: ulid.New(), TenantID: valueobjects.DefaultTenant,
			State: StatePublishing, Version: 1, UpdatedAt: now.Add(-time.Hour),
		}}
		i := &Interactor{store: store, values: &applier{}, now: func() time.Time { return now }}

		Convey("When it is rejected", func() {
			_, err := i.Reject(context.Background(), store.cs.ID.String())

			Convey("Then the refusal names the reclaim path", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "publish it again")
			})
		})
	})
}
