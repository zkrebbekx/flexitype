package typedef

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// fakeAttrs is a minimal in-memory attribute repository for effective-set
// resolution.
type fakeAttrs struct {
	byType map[string][]*domainattribute.Definition
}

func (r *fakeAttrs) WithTx(db.QueryExecer) domainattribute.Repository { return r }
func (r *fakeAttrs) Get(context.Context, valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	return nil, domainerrors.NewNotFound(domainattribute.AggregateType, "unused")
}
func (r *fakeAttrs) GetMany(context.Context, []valueobjects.AttributeDefinitionID) ([]*domainattribute.Definition, error) {
	return nil, nil
}
func (r *fakeAttrs) GetForUpdate(context.Context, valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	return nil, domainerrors.NewNotFound(domainattribute.AggregateType, "unused")
}
func (r *fakeAttrs) GetByInternalName(_ context.Context, typeDefID valueobjects.TypeDefinitionID, name string) (*domainattribute.Definition, error) {
	for _, a := range r.byType[typeDefID.String()] {
		if a.InternalName() == name {
			return a, nil
		}
	}
	return nil, domainerrors.NewNotFound(domainattribute.AggregateType, name)
}
func (r *fakeAttrs) ListByTypeDefinition(_ context.Context, typeDefID valueobjects.TypeDefinitionID, _ db.Page) ([]*domainattribute.Definition, int, error) {
	attrs := r.byType[typeDefID.String()]
	return attrs, len(attrs), nil
}
func (r *fakeAttrs) List(context.Context, domainattribute.Filter, db.Page) ([]*domainattribute.Definition, int, error) {
	return nil, 0, nil
}
func (r *fakeAttrs) Save(_ context.Context, a *domainattribute.Definition) error {
	key := a.TypeDefinitionID().String()
	r.byType[key] = append(r.byType[key], a)
	return nil
}

func mustType(t *testing.T, repo *fakeRepo, name string, extends *domaintypedef.TypeDefinition) *domaintypedef.TypeDefinition {
	t.Helper()
	td, _, err := domaintypedef.New(domaintypedef.NewInput{
		InternalName: name, DisplayName: name, Extends: extends,
	}, time.Now())
	if err != nil {
		t.Fatalf("new type: %v", err)
	}
	if err := repo.Save(context.Background(), td); err != nil {
		t.Fatalf("save type: %v", err)
	}
	return td
}

func mustAttr(t *testing.T, repo *fakeAttrs, owner *domaintypedef.TypeDefinition, name string) *domainattribute.Definition {
	t.Helper()
	a, _, err := domainattribute.New(domainattribute.NewInput{
		TypeDefinitionID: owner.ID(),
		InternalName:     name,
		DisplayName:      name,
		DataType:         valueobjects.DataTypeString,
	}, time.Now())
	if err != nil {
		t.Fatalf("new attribute: %v", err)
	}
	if err := repo.Save(context.Background(), a); err != nil {
		t.Fatalf("save attribute: %v", err)
	}
	return a
}

func TestTypeHierarchy(t *testing.T) {
	Convey("Given a Product ← Bike ← MountainBike hierarchy", t, func() {
		ctx := context.Background()
		types := newFakeRepo()
		attrs := &fakeAttrs{byType: map[string][]*domainattribute.Definition{}}

		product := mustType(t, types, "product", nil)
		bike := mustType(t, types, "bike", product)
		mtb := mustType(t, types, "mountain_bike", bike)

		mustAttr(t, attrs, product, "sku")
		mustAttr(t, attrs, bike, "wheel_size")
		mustAttr(t, attrs, mtb, "suspension")

		Convey("When walking the hierarchy", func() {
			ancestors, err := Ancestors(ctx, types, mtb)
			So(err, ShouldBeNil)
			descendants, err := Descendants(ctx, types, product)
			So(err, ShouldBeNil)
			root, err := Root(ctx, types, mtb)
			So(err, ShouldBeNil)

			Convey("Then ancestors, descendants and root resolve in order", func() {
				So(ancestors, ShouldHaveLength, 2)
				So(ancestors[0].InternalName(), ShouldEqual, "bike")
				So(ancestors[1].InternalName(), ShouldEqual, "product")
				So(descendants, ShouldHaveLength, 2)
				So(root.InternalName(), ShouldEqual, "product")
			})
		})

		Convey("When resolving effective attributes for the subtype", func() {
			interactor := NewInteractor(uow.New(&fakeTransactor{}, events.NewDispatcher(), &fakeActivityLog{}), types, attrs)
			effective, err := interactor.EffectiveAttributes(ctx, mtb.ID().String())

			Convey("Then own attributes come first and every ancestor's follow, tagged with the declaring type", func() {
				So(err, ShouldBeNil)
				So(effective, ShouldHaveLength, 3)
				So(effective[0].Attribute.InternalName, ShouldEqual, "suspension")
				So(effective[0].DeclaredIn.InternalName, ShouldEqual, "mountain_bike")
				So(effective[1].Attribute.InternalName, ShouldEqual, "wheel_size")
				So(effective[1].DeclaredIn.InternalName, ShouldEqual, "bike")
				So(effective[2].Attribute.InternalName, ShouldEqual, "sku")
				So(effective[2].DeclaredIn.InternalName, ShouldEqual, "product")
			})
		})

		Convey("When checking ancestry", func() {
			ok, err := IsAncestorOrSelf(ctx, types, mtb, product.ID())
			So(err, ShouldBeNil)
			notOk, err := IsAncestorOrSelf(ctx, types, product, mtb.ID())
			So(err, ShouldBeNil)

			Convey("Then the relation is directional", func() {
				So(ok, ShouldBeTrue)
				So(notOk, ShouldBeFalse)
			})
		})

		Convey("When a type would extend a relationship attribute set", func() {
			attrSet, _, err := domaintypedef.NewAttributeSet(valueobjects.DefaultTenant, "rel_x_attrs", "x", time.Now())
			So(err, ShouldBeNil)

			_, _, err = domaintypedef.New(domaintypedef.NewInput{
				InternalName: "bad", DisplayName: "bad", Extends: attrSet,
			}, time.Now())

			Convey("Then creation is rejected", func() {
				So(domainerrors.IsValidation(err), ShouldBeTrue)
			})
		})
	})
}
