package value

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// --- in-memory fakes ---------------------------------------------------------

type fakeTransactor struct {
	pre, post, rollback []db.Hook
}

func (f *fakeTransactor) GetContext(context.Context, any, string, ...any) error {
	return fmt.Errorf("fake: no SQL")
}
func (f *fakeTransactor) SelectContext(context.Context, any, string, ...any) error {
	return fmt.Errorf("fake: no SQL")
}
func (f *fakeTransactor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, fmt.Errorf("fake: no SQL")
}
func (f *fakeTransactor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("fake: no SQL")
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

type fakeAttrRepo struct {
	defs map[string]*domainattribute.Definition
}

func (r *fakeAttrRepo) WithTx(db.QueryExecer) domainattribute.Repository { return r }

func (r *fakeAttrRepo) Get(_ context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	def, ok := r.defs[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domainattribute.AggregateType, id.String())
	}
	return def, nil
}

func (r *fakeAttrRepo) GetMany(ctx context.Context, ids []valueobjects.AttributeDefinitionID) ([]*domainattribute.Definition, error) {
	out := make([]*domainattribute.Definition, 0, len(ids))
	for _, id := range ids {
		def, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, nil
}

func (r *fakeAttrRepo) GetForUpdate(ctx context.Context, id valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	return r.Get(ctx, id)
}

func (r *fakeAttrRepo) GetByInternalName(context.Context, valueobjects.TypeDefinitionID, string) (*domainattribute.Definition, error) {
	return nil, domainerrors.NewNotFound(domainattribute.AggregateType, "unused")
}

func (r *fakeAttrRepo) ListByTypeDefinition(context.Context, valueobjects.TypeDefinitionID, db.Page) ([]*domainattribute.Definition, int, error) {
	return nil, 0, nil
}

func (r *fakeAttrRepo) List(context.Context, domainattribute.Filter, db.Page) ([]*domainattribute.Definition, int, error) {
	return nil, 0, nil
}

func (r *fakeAttrRepo) Save(_ context.Context, a *domainattribute.Definition) error {
	r.defs[a.ID().String()] = a
	return nil
}

type fakeValueRepo struct {
	values map[string]domainvalue.Snapshot
}

func (r *fakeValueRepo) WithTx(db.QueryExecer) domainvalue.Repository { return r }

func (r *fakeValueRepo) Get(_ context.Context, id valueobjects.AttributeValueID) (*domainvalue.AttributeValue, error) {
	snap, ok := r.values[id.String()]
	if !ok {
		return nil, domainerrors.NewNotFound(domainvalue.AggregateType, id.String())
	}
	return domainvalue.Rehydrate(snap), nil
}

func (r *fakeValueRepo) GetForUpdate(ctx context.Context, id valueobjects.AttributeValueID) (*domainvalue.AttributeValue, error) {
	return r.Get(ctx, id)
}

func (r *fakeValueRepo) ListByEntity(_ context.Context, key domainvalue.EntityKey) ([]*domainvalue.AttributeValue, error) {
	var out []*domainvalue.AttributeValue
	for _, snap := range r.values {
		if snap.EntityID == key.EntityID && snap.TypeDefinitionID.Equals(key.TypeDefinitionID) && snap.ArchivedAt == nil {
			out = append(out, domainvalue.Rehydrate(snap))
		}
	}
	return out, nil
}

func (r *fakeValueRepo) ListByDefinition(context.Context, valueobjects.AttributeDefinitionID, db.Page) ([]*domainvalue.AttributeValue, int, error) {
	return nil, 0, nil
}

func (r *fakeValueRepo) FindByDefinitionAndEntity(_ context.Context, defID valueobjects.AttributeDefinitionID, entityID valueobjects.EntityID) ([]*domainvalue.AttributeValue, error) {
	var out []*domainvalue.AttributeValue
	for _, snap := range r.values {
		if snap.AttributeDefinitionID.Equals(defID) && snap.EntityID == entityID && snap.ArchivedAt == nil {
			out = append(out, domainvalue.Rehydrate(snap))
		}
	}
	return out, nil
}

func (r *fakeValueRepo) CountByDefinitionAndValue(_ context.Context, defID valueobjects.AttributeDefinitionID, v valueobjects.Value, exclude valueobjects.EntityID) (int, error) {
	count := 0
	for _, snap := range r.values {
		if snap.AttributeDefinitionID.Equals(defID) && snap.EntityID != exclude && snap.ArchivedAt == nil && snap.Value.Equal(v) {
			count++
		}
	}
	return count, nil
}

func (r *fakeValueRepo) List(context.Context, domainvalue.Filter, db.Page) ([]*domainvalue.AttributeValue, int, error) {
	return nil, 0, nil
}

func (r *fakeValueRepo) Save(_ context.Context, av *domainvalue.AttributeValue) error {
	r.values[av.ID().String()] = av.Snapshot()
	return nil
}

type fakeDepRepo struct {
	deps []*domaindependency.Dependency
}

func (r *fakeDepRepo) WithTx(db.QueryExecer) domaindependency.Repository { return r }

func (r *fakeDepRepo) Get(context.Context, valueobjects.DependencyID) (*domaindependency.Dependency, error) {
	return nil, domainerrors.NewNotFound(domaindependency.AggregateType, "unused")
}

func (r *fakeDepRepo) GetForUpdate(context.Context, valueobjects.DependencyID) (*domaindependency.Dependency, error) {
	return nil, domainerrors.NewNotFound(domaindependency.AggregateType, "unused")
}

func (r *fakeDepRepo) ListByTarget(_ context.Context, targetID valueobjects.AttributeDefinitionID) ([]*domaindependency.Dependency, error) {
	var out []*domaindependency.Dependency
	for _, d := range r.deps {
		if d.TargetAttributeID().Equals(targetID) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r *fakeDepRepo) ListBySource(context.Context, valueobjects.AttributeDefinitionID) ([]*domaindependency.Dependency, error) {
	return nil, nil
}

func (r *fakeDepRepo) List(context.Context, domaindependency.Filter, db.Page) ([]*domaindependency.Dependency, int, error) {
	return nil, 0, nil
}

func (r *fakeDepRepo) Save(_ context.Context, d *domaindependency.Dependency) error {
	r.deps = append(r.deps, d)
	return nil
}

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

// --- helpers ------------------------------------------------------------------

func mustAttr(in domainattribute.NewInput) *domainattribute.Definition {
	def, _, err := domainattribute.New(in, time.Now())
	So(err, ShouldBeNil)
	return def
}

func enumOf(members ...string) domainattribute.Constraints {
	values := make([]valueobjects.Value, 0, len(members))
	for _, m := range members {
		values = append(values, valueobjects.NewEnumValue(m))
	}
	return domainattribute.Constraints{domainattribute.OneOf{Values: values}}
}

// --- tests ----------------------------------------------------------------------

func TestSetValueUsecase(t *testing.T) {
	Convey("Given the value usecases over in-memory repositories", t, func() {
		typeDef := valueobjects.NewTypeDefinitionID()
		attrs := &fakeAttrRepo{defs: map[string]*domainattribute.Definition{}}
		values := &fakeValueRepo{values: map[string]domainvalue.Snapshot{}}
		deps := &fakeDepRepo{}
		log := &fakeActivityLog{}
		dispatcher := events.NewDispatcher()

		var dispatched []events.Envelope
		dispatcher.RegisterFunc("recorder", func(_ context.Context, env events.Envelope) error {
			dispatched = append(dispatched, env)
			return nil
		})

		unit := uow.New(&fakeTransactor{}, dispatcher, log)
		interactor := NewInteractor(unit, attrs, values, deps)
		ctx := context.Background()

		serial := mustAttr(domainattribute.NewInput{
			TypeDefinitionID: typeDef,
			InternalName:     "serial",
			DisplayName:      "Serial",
			DataType:         valueobjects.DataTypeString,
			Unique:           true,
		})
		So(attrs.Save(ctx, serial), ShouldBeNil)

		tags := mustAttr(domainattribute.NewInput{
			TypeDefinitionID: typeDef,
			InternalName:     "tags",
			DisplayName:      "Tags",
			DataType:         valueobjects.DataTypeString,
			MultiValued:      true,
		})
		So(attrs.Save(ctx, tags), ShouldBeNil)

		Convey("When a value is set for the first time", func() {
			snap, err := interactor.Set(ctx, SetInput{
				AttributeDefinitionID: serial.ID().String(),
				EntityID:              "order-1",
				Value:                 json.RawMessage(`"SN-100"`),
			})

			Convey("Then it persists, audits and dispatches a set event", func() {
				So(err, ShouldBeNil)
				So(snap.Value.Text(), ShouldEqual, "SN-100")
				So(values.values, ShouldHaveLength, 1)
				So(log.entries, ShouldHaveLength, 1)
				So(dispatched, ShouldHaveLength, 1)
				So(dispatched[0].Type, ShouldEqual, "flexitype.attribute_value.set")
			})
		})

		Convey("When a single-valued attribute is set twice for one entity", func() {
			first, err := interactor.Set(ctx, SetInput{
				AttributeDefinitionID: serial.ID().String(),
				EntityID:              "order-1",
				Value:                 json.RawMessage(`"SN-100"`),
			})
			So(err, ShouldBeNil)

			second, err := interactor.Set(ctx, SetInput{
				AttributeDefinitionID: serial.ID().String(),
				EntityID:              "order-1",
				Value:                 json.RawMessage(`"SN-200"`),
			})

			Convey("Then the existing value updates in place", func() {
				So(err, ShouldBeNil)
				So(second.ID.String(), ShouldEqual, first.ID.String())
				So(second.Value.Text(), ShouldEqual, "SN-200")
				So(values.values, ShouldHaveLength, 1)
			})
		})

		Convey("When a multi-valued attribute is set twice with different values", func() {
			_, err := interactor.Set(ctx, SetInput{
				AttributeDefinitionID: tags.ID().String(),
				EntityID:              "order-1",
				Value:                 json.RawMessage(`"fragile"`),
			})
			So(err, ShouldBeNil)

			_, err = interactor.Set(ctx, SetInput{
				AttributeDefinitionID: tags.ID().String(),
				EntityID:              "order-1",
				Value:                 json.RawMessage(`"priority"`),
			})
			So(err, ShouldBeNil)

			Convey("Then both values coexist on the entity", func() {
				So(values.values, ShouldHaveLength, 2)
			})
		})

		Convey("When a unique value is reused by another entity", func() {
			_, err := interactor.Set(ctx, SetInput{
				AttributeDefinitionID: serial.ID().String(),
				EntityID:              "order-1",
				Value:                 json.RawMessage(`"SN-100"`),
			})
			So(err, ShouldBeNil)

			_, err = interactor.Set(ctx, SetInput{
				AttributeDefinitionID: serial.ID().String(),
				EntityID:              "order-2",
				Value:                 json.RawMessage(`"SN-100"`),
			})

			Convey("Then the write conflicts", func() {
				So(domainerrors.IsConflict(err), ShouldBeTrue)
			})
		})

		Convey("When a dependency restricts the target attribute", func() {
			category := mustAttr(domainattribute.NewInput{
				TypeDefinitionID: typeDef,
				InternalName:     "category",
				DisplayName:      "Category",
				DataType:         valueobjects.DataTypeEnum,
				Constraints:      enumOf("bike", "car"),
			})
			So(attrs.Save(ctx, category), ShouldBeNil)

			subcategory := mustAttr(domainattribute.NewInput{
				TypeDefinitionID: typeDef,
				InternalName:     "subcategory",
				DisplayName:      "Subcategory",
				DataType:         valueobjects.DataTypeEnum,
				Constraints:      enumOf("mountain", "road", "sedan"),
			})
			So(attrs.Save(ctx, subcategory), ShouldBeNil)

			bike := valueobjects.NewEnumValue("bike")
			dep, _, err := domaindependency.New(domaindependency.NewInput{
				Source:     category,
				Target:     subcategory,
				Conditions: []domaindependency.Condition{{Kind: domaindependency.CondEquals, Value: &bike}},
				Effect: domaindependency.Effect{AllowedValues: []valueobjects.Value{
					valueobjects.NewEnumValue("mountain"),
					valueobjects.NewEnumValue("road"),
				}},
			}, time.Now())
			So(err, ShouldBeNil)
			So(deps.Save(ctx, dep), ShouldBeNil)

			_, err = interactor.Set(ctx, SetInput{
				AttributeDefinitionID: category.ID().String(),
				EntityID:              "product-9",
				Value:                 json.RawMessage(`"bike"`),
			})
			So(err, ShouldBeNil)

			Convey("Then values allowed by the matched dependency pass", func() {
				_, err := interactor.Set(ctx, SetInput{
					AttributeDefinitionID: subcategory.ID().String(),
					EntityID:              "product-9",
					Value:                 json.RawMessage(`"mountain"`),
				})
				So(err, ShouldBeNil)
			})

			Convey("Then values outside the matched dependency are rejected", func() {
				_, err := interactor.Set(ctx, SetInput{
					AttributeDefinitionID: subcategory.ID().String(),
					EntityID:              "product-9",
					Value:                 json.RawMessage(`"sedan"`),
				})
				So(domainerrors.IsDependencyViolation(err), ShouldBeTrue)
			})
		})

		Convey("When a mistyped value arrives", func() {
			_, err := interactor.Set(ctx, SetInput{
				AttributeDefinitionID: serial.ID().String(),
				EntityID:              "order-1",
				Value:                 json.RawMessage(`123`),
			})

			Convey("Then the write fails validation and nothing dispatches", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
				So(dispatched, ShouldBeEmpty)
				So(log.entries, ShouldBeEmpty)
			})
		})
	})
}
