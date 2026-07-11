package typedef

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// fakeTransactor implements db.Transactor in memory, honouring the hook
// contract: pre-commit before commit (errors roll back), post-commit after,
// rollback hooks on rollback.
type fakeTransactor struct {
	committed  bool
	rolledBack bool
	pre        []db.Hook
	post       []db.Hook
	rollback   []db.Hook
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

// Begin starts a fresh logical transaction: hooks from a previous unit of
// work must not leak into the next one.
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
	f.committed = true
	for _, h := range f.post {
		if err := h(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeTransactor) Rollback(ctx context.Context) error {
	f.rolledBack = true
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

// fakeRepo is an in-memory typedef.Repository.
type fakeRepo struct {
	items map[string]domaintypedef.Snapshot
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{items: make(map[string]domaintypedef.Snapshot)}
}

func (r *fakeRepo) WithTx(db.QueryExecer) domaintypedef.Repository { return r }

func (r *fakeRepo) Get(_ context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	snap, ok := r.items[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, id.String())
	}
	return domaintypedef.Rehydrate(snap), nil
}

func (r *fakeRepo) GetForUpdate(ctx context.Context, id valueobjects.TypeDefinitionID) (*domaintypedef.TypeDefinition, error) {
	return r.Get(ctx, id)
}

func (r *fakeRepo) GetByInternalName(_ context.Context, tenant valueobjects.TenantID, name string) (*domaintypedef.TypeDefinition, error) {
	for _, snap := range r.items {
		if snap.TenantID == tenant && snap.InternalName == name && snap.ArchivedAt == nil {
			return domaintypedef.Rehydrate(snap), nil
		}
	}
	return nil, domainerrors.NewNotFound(domaintypedef.AggregateType, name)
}

func (r *fakeRepo) List(context.Context, domaintypedef.Filter, db.Page) ([]*domaintypedef.TypeDefinition, int, error) {
	out := make([]*domaintypedef.TypeDefinition, 0, len(r.items))
	for _, snap := range r.items {
		out = append(out, domaintypedef.Rehydrate(snap))
	}
	return out, len(out), nil
}

func (r *fakeRepo) ListChildren(_ context.Context, parentID valueobjects.TypeDefinitionID) ([]*domaintypedef.TypeDefinition, error) {
	var out []*domaintypedef.TypeDefinition
	for _, snap := range r.items {
		if snap.ExtendsID != nil && snap.ExtendsID.Equals(parentID) {
			out = append(out, domaintypedef.Rehydrate(snap))
		}
	}
	return out, nil
}

func (r *fakeRepo) Save(_ context.Context, t *domaintypedef.TypeDefinition) error {
	r.items[t.ID().String()] = t.Snapshot()
	return nil
}

// fakeActivityLog records written entries.
type fakeActivityLog struct {
	entries []activity.Entry
}

func (l *fakeActivityLog) Write(_ context.Context, _ db.QueryExecer, entries []activity.Entry) error {
	l.entries = append(l.entries, entries...)
	return nil
}

func (l *fakeActivityLog) List(context.Context, activity.Filter, db.Page) ([]activity.Entry, int, error) {
	return l.entries, len(l.entries), nil
}

func TestCreateTypeDefinitionUsecase(t *testing.T) {
	Convey("Given the create usecase wired through a real unit of work", t, func() {
		transactor := &fakeTransactor{}
		repo := newFakeRepo()
		log := &fakeActivityLog{}
		dispatcher := events.NewDispatcher()

		var dispatched []events.Envelope
		dispatcher.RegisterFunc("recorder", func(_ context.Context, env events.Envelope) error {
			dispatched = append(dispatched, env)
			return nil
		})

		unit := uow.New(transactor, dispatcher, log,
			uow.WithNow(func() time.Time { return time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC) }))
		interactor := NewInteractor(unit, repo, nil)

		ctx := uow.WithActor(context.Background(), uow.Actor{
			ID: "sa-1", Name: "ci-importer", Kind: uow.ActorServiceAccount,
		})
		ctx = uow.WithTenant(ctx, valueobjects.TenantID("acme"))

		Convey("When a type definition is created", func() {
			snap, err := interactor.Create(ctx, CreateInput{
				InternalName: "product",
				DisplayName:  "Product",
			})

			Convey("Then the aggregate persists inside a committed transaction", func() {
				So(err, ShouldBeNil)
				So(snap.InternalName, ShouldEqual, "product")
				So(snap.TenantID.String(), ShouldEqual, "acme")
				So(transactor.committed, ShouldBeTrue)
				So(repo.items, ShouldHaveLength, 1)
			})

			Convey("Then the pre-commit handler wrote an activity entry with the after descriptor", func() {
				So(log.entries, ShouldHaveLength, 1)
				entry := log.entries[0]
				So(entry.Entity, ShouldEqual, "type_definition")
				So(entry.Action, ShouldEqual, activity.ActionCreated)
				So(entry.Actor, ShouldEqual, "service_account:ci-importer")
				So(entry.TenantID.String(), ShouldEqual, "acme")
				So(entry.Before, ShouldBeEmpty)
				So(string(entry.After), ShouldContainSubstring, `"internal_name":"product"`)
			})

			Convey("Then the domain event dispatched after commit with envelope metadata", func() {
				So(dispatched, ShouldHaveLength, 1)
				env := dispatched[0]
				So(env.Type, ShouldEqual, domaintypedef.EventCreated)
				So(env.TenantID, ShouldEqual, "acme")
				So(env.Actor, ShouldEqual, "service_account:ci-importer")
				So(env.AggregateID, ShouldEqual, snap.ID.String())
			})
		})

		Convey("When the internal name is already taken", func() {
			_, err := interactor.Create(ctx, CreateInput{InternalName: "product", DisplayName: "Product"})
			So(err, ShouldBeNil)
			dispatched = nil
			log.entries = nil

			duplicate := &fakeTransactor{}
			unit2 := uow.New(duplicate, dispatcher, log)
			interactor2 := NewInteractor(unit2, repo, nil)
			_, err = interactor2.Create(ctx, CreateInput{InternalName: "product", DisplayName: "Product 2"})

			Convey("Then the usecase conflicts, rolls back, and no events or audit rows leak", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
				So(duplicate.rolledBack, ShouldBeTrue)
				So(duplicate.committed, ShouldBeFalse)
				So(dispatched, ShouldBeEmpty)
				So(log.entries, ShouldBeEmpty)
			})
		})

		Convey("When an update changes nothing", func() {
			snap, err := interactor.Create(ctx, CreateInput{InternalName: "part", DisplayName: "Part"})
			So(err, ShouldBeNil)
			dispatched = nil
			log.entries = nil

			out, err := interactor.Update(ctx, UpdateInput{ID: snap.ID.String(), DisplayName: "Part"})

			Convey("Then it is a no-op: same version, no events, no audit entries", func() {
				So(err, ShouldBeNil)
				So(out.Version, ShouldEqual, 1)
				So(dispatched, ShouldBeEmpty)
				So(log.entries, ShouldBeEmpty)
			})
		})

		Convey("When a definition is archived", func() {
			snap, err := interactor.Create(ctx, CreateInput{InternalName: "asset", DisplayName: "Asset"})
			So(err, ShouldBeNil)
			dispatched = nil
			log.entries = nil

			out, err := interactor.Archive(ctx, snap.ID.String())

			Convey("Then the archive persists with audit before/after descriptors", func() {
				So(err, ShouldBeNil)
				So(out.ArchivedAt, ShouldNotBeNil)
				So(log.entries, ShouldHaveLength, 1)
				So(log.entries[0].Action, ShouldEqual, activity.ActionArchived)
				So(string(log.entries[0].Before), ShouldNotContainSubstring, `"archived_at"`)
				So(string(log.entries[0].After), ShouldContainSubstring, `"archived_at"`)
				So(dispatched, ShouldHaveLength, 1)
				So(dispatched[0].Type, ShouldEqual, domaintypedef.EventArchived)
			})
		})
	})
}
