package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// fakeOpsStore records the filters the usecase builds and serves canned
// parked rows, so tenant stamping, id validation and paging are observable
// without a database.
type fakeOpsStore struct {
	lastFilter ParkedFilter
	lastPage   db.Page
	rows       []ParkedEnvelope
	total      int
	redriven   int
	err        error
}

func (s *fakeOpsStore) ListParked(_ context.Context, f ParkedFilter, p db.Page) ([]ParkedEnvelope, int, error) {
	s.lastFilter, s.lastPage = f, p
	return s.rows, s.total, s.err
}

func (s *fakeOpsStore) Redrive(_ context.Context, _ db.Transactor, f ParkedFilter) (int, error) {
	s.lastFilter = f
	return s.redriven, s.err
}

// hookTx is a minimal db.Transactor for the unit of work: it runs pre-commit
// hooks on Commit and nothing else. The memory backend cannot serve here —
// importing it from this package would close an import cycle through the
// application factory.
type hookTx struct {
	db.TxMarker
	pre []db.Hook
}

func (t *hookTx) Begin(context.Context) (db.Transactor, error) { return t, nil }

func (t *hookTx) Commit(ctx context.Context) error {
	for _, h := range t.pre {
		if err := h(ctx); err != nil {
			return err
		}
	}
	t.pre = nil
	return nil
}

func (t *hookTx) Rollback(context.Context) error { t.pre = nil; return nil }

func (t *hookTx) InTransaction(ctx context.Context, fn func(db.Transactor) error) error {
	if err := fn(t); err != nil {
		t.pre = nil
		return err
	}
	return t.Commit(ctx)
}

func (t *hookTx) OnPreCommit(h db.Hook) { t.pre = append(t.pre, h) }
func (t *hookTx) OnPostCommit(db.Hook)  {}
func (t *hookTx) OnRollback(db.Hook)    {}

// recordingLog captures the activity entries the unit of work persists at
// pre-commit, so the redrive audit trail is assertable.
type recordingLog struct {
	entries []activity.Entry
}

func (l *recordingLog) Write(_ context.Context, _ db.Tx, entries []activity.Entry) error {
	l.entries = append(l.entries, entries...)
	return nil
}

func (l *recordingLog) List(context.Context, activity.Filter, db.Page) ([]activity.Entry, int, error) {
	return nil, 0, nil
}

// TestOutboxOps covers the parked-envelope recovery usecases (#478): the
// listing that made parked envelopes visible and the redrive that made them
// deliverable again.
func TestOutboxOps(t *testing.T) {
	tenant := valueobjects.TenantID("acme")

	newOps := func(store *fakeOpsStore, log *recordingLog, nudged *int) *Ops {
		unit := uow.New(&hookTx{}, events.NewDispatcher(), log)
		return NewOps(unit, store, func() { *nudged++ })
	}

	Convey("Given the outbox recovery usecases over a recording store", t, func() {
		store := &fakeOpsStore{}
		log := &recordingLog{}
		nudged := 0
		ops := newOps(store, log, &nudged)
		ctx := uow.WithTenant(context.Background(), tenant)

		Convey("When the parked listing runs with an event-type filter", func() {
			store.rows = []ParkedEnvelope{{ID: ulid.New().String(), EventType: "flexitype.entity.updated", Attempts: 25, ParkedAt: time.Now()}}
			out, err := ops.ListParked(ctx, ListParkedInput{EventType: "flexitype.entity.updated"})

			Convey("Then the page returns and the filter carries the tenant and the type", func() {
				So(err, ShouldBeNil)
				So(out.Items, ShouldHaveLength, 1)
				So(store.lastFilter.TenantID, ShouldEqual, tenant)
				So(store.lastFilter.EventType, ShouldEqual, "flexitype.entity.updated")
				So(store.lastFilter.ID, ShouldBeEmpty)
			})
		})

		Convey("When the listing is narrowed to one malformed envelope id", func() {
			out, err := ops.ListParked(ctx, ListParkedInput{ID: "not-a-ulid"})

			Convey("Then it is rejected as a validation error before the store is asked", func() {
				So(out, ShouldBeNil)
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When a redrive moves envelopes", func() {
			store.redriven = 3
			n, err := ops.Redrive(ctx, RedriveInput{EventType: "flexitype.entity.updated"})

			Convey("Then the count returns and the tenant-stamped filter reached the store", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 3)
				So(store.lastFilter.TenantID, ShouldEqual, tenant)
			})

			Convey("Then the redrive is on the audit trail", func() {
				So(log.entries, ShouldHaveLength, 1)
				So(log.entries[0].Entity, ShouldEqual, EntityName)
				So(log.entries[0].EntityID, ShouldEqual, "flexitype.entity.updated")
				So(log.entries[0].Action, ShouldEqual, activity.ActionRestored)
				So(log.entries[0].TenantID, ShouldEqual, tenant)
			})

			Convey("Then the relay is nudged so delivery restarts at once", func() {
				So(nudged, ShouldEqual, 1)
			})
		})

		Convey("When a redrive matches nothing", func() {
			store.redriven = 0
			n, err := ops.Redrive(ctx, RedriveInput{})

			Convey("Then no audit entry is written and the relay is left alone", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 0)
				So(log.entries, ShouldBeEmpty)
				So(nudged, ShouldEqual, 0)
			})
		})

		Convey("When a redrive targets a malformed envelope id", func() {
			_, err := ops.Redrive(ctx, RedriveInput{ID: "nope"})

			Convey("Then it is rejected as a validation error", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})

		Convey("When the store fails a redrive", func() {
			store.err = errors.New("boom")
			_, err := ops.Redrive(ctx, RedriveInput{})

			Convey("Then the error surfaces and the relay is not nudged", func() {
				So(err, ShouldNotBeNil)
				So(nudged, ShouldEqual, 0)
			})
		})

		Convey("When a redrive without a nudge hook moves envelopes", func() {
			quiet := NewOps(uow.New(&hookTx{}, events.NewDispatcher(), log), store, nil)
			store.redriven = 1

			Convey("Then it completes without panicking", func() {
				So(func() { _, _ = quiet.Redrive(ctx, RedriveInput{}) }, ShouldNotPanic)
			})
		})
	})
}
