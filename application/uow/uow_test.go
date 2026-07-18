package uow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// fakeTx is a minimal Transactor for exercising the unit-of-work commit
// lifecycle without a database: Begin returns itself, Commit runs the
// registered pre-commit hooks (aborting on error), marks the change durable,
// then runs post-commit hooks collecting their errors — the same ordering the
// real sqlx transactor uses.
type fakeTx struct {
	db.TxMarker
	preCommit  []db.Hook
	postCommit []db.Hook
	rollback   []db.Hook
	committed  bool
	rolledBack bool
}

func (f *fakeTx) Begin(context.Context) (db.Transactor, error) { return f, nil }
func (f *fakeTx) OnPreCommit(h db.Hook)                        { f.preCommit = append(f.preCommit, h) }
func (f *fakeTx) OnPostCommit(h db.Hook)                       { f.postCommit = append(f.postCommit, h) }
func (f *fakeTx) OnRollback(h db.Hook)                         { f.rollback = append(f.rollback, h) }

func (f *fakeTx) Commit(ctx context.Context) error {
	for _, h := range f.preCommit {
		if err := h(ctx); err != nil {
			_ = f.Rollback(ctx)
			return err
		}
	}
	f.committed = true
	var errs []error
	for _, h := range f.postCommit {
		if err := h(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f *fakeTx) Rollback(ctx context.Context) error {
	f.rolledBack = true
	for _, h := range f.rollback {
		_ = h(ctx)
	}
	return nil
}

func (f *fakeTx) InTransaction(ctx context.Context, fn func(tx db.Transactor) error) error {
	if err := fn(f); err != nil {
		_ = f.Rollback(ctx)
		return err
	}
	return f.Commit(ctx)
}

// fakeEvent is a trivial domain event to drive a dispatch.
type fakeEvent struct{}

func (fakeEvent) EventType() events.Type  { return "flexitype.test.happened" }
func (fakeEvent) AggregateType() string   { return "test" }
func (fakeEvent) AggregateID() string     { return "agg-1" }
func (fakeEvent) OccurredWhen() time.Time { return time.Unix(0, 0).UTC() }

func TestInternalProjectionFailureSurfaced(t *testing.T) {
	Convey("Given a unit of work whose internal projection always fails", t, func() {
		ctx := context.Background()

		projections := events.NewDispatcher()
		projections.RegisterFunc("boom-projection", func(context.Context, events.Envelope) error {
			return errors.New("projection exploded")
		})

		var observed error
		tx := &fakeTx{}
		u := uow.New(tx, events.NewDispatcher(), nil,
			uow.WithProjections(projections),
			uow.WithDispatchObserver(func(_ context.Context, err error) { observed = err }),
		)

		Convey("When a committed unit of work emits an event that drives the projection", func() {
			err := u.Execute(ctx, func(_ db.Transactor, c *uow.Collector) error {
				c.CollectEvents(fakeEvent{})
				return nil
			})

			Convey("Then the write still commits — a post-commit projection failure never fails the request", func() {
				So(err, ShouldBeNil)
				So(tx.committed, ShouldBeTrue)
				So(tx.rolledBack, ShouldBeFalse)
			})

			Convey("And the failure is surfaced to the observer, not silently swallowed into permanent staleness", func() {
				So(observed, ShouldNotBeNil)
				So(observed.Error(), ShouldContainSubstring, "projection exploded")
			})
		})
	})
}
