package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// TestStructuralGuardsUseTheRightShape covers three guards that read the
// stored data wrongly and so refused, or allowed, the wrong change.
func TestStructuralGuardsUseTheRightShape(t *testing.T) {
	Convey("Given a localizable attribute holding one value per locale", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		title, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "title",
			DisplayName: "Title", DataType: "string", Localizable: true,
		})
		So(err, ShouldBeNil)
		for _, locale := range []string{"en-GB", "fr-FR"} {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: title.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(), Locale: locale,
				Value: json.RawMessage(`"t"`),
			})
			So(serr, ShouldBeNil)
		}

		Convey("When it is made single-valued", func() {
			// One value per locale is not "more than one value" for this
			// purpose: the new schema expresses that data perfectly. Counting
			// it as such refused the migration and left deleting real data as
			// the only way through.
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: title.ID.String(), DisplayName: "Title", Localizable: true,
			})

			Convey("Then it is allowed", func() {
				So(uerr, ShouldBeNil)
			})
		})

		Convey("When one entity genuinely holds two values in one scope", func() {
			multi, merr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "tags",
				DisplayName: "Tags", DataType: "string", MultiValued: true,
			})
			So(merr, ShouldBeNil)
			for _, v := range []string{`"a"`, `"b"`} {
				_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: multi.ID.String(), EntityID: "p1",
					TypeDefinitionID: product.ID.String(), Value: json.RawMessage(v),
				})
				So(serr, ShouldBeNil)
			}

			Convey("Then making it single-valued is still refused", func() {
				_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
					ID: multi.ID.String(), DisplayName: "Tags",
				})
				So(domainerrors.IsConflict(uerr), ShouldBeTrue)
			})
		})
	})

	Convey("Given three distinct integer values on one attribute", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		rank, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "rank",
			DisplayName: "Rank", DataType: "integer",
		})
		So(err, ShouldBeNil)
		for i, entity := range []string{"p1", "p2", "p3"} {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: rank.ID.String(), EntityID: entity,
				TypeDefinitionID: product.ID.String(),
				Value:            json.RawMessage([]byte{byte('0' + i + 1)}),
			})
			So(serr, ShouldBeNil)
		}

		Convey("When the attribute is made unique", func() {
			// The in-memory backend keyed duplicates on Text(), which is ""
			// for every non-text type — so three distinct integers read as
			// three copies of one value and the flip was refused, while
			// Postgres allowed it. The backends disagreed on both the data
			// and the semantics.
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: rank.ID.String(), DisplayName: "Rank", Unique: true,
			})

			Convey("Then it is allowed, because the values differ", func() {
				So(uerr, ShouldBeNil)
			})
		})
	})
}

// TestArchivedAncestorStopsWrites covers the chain walk the write guard was
// missing.
func TestArchivedAncestorStopsWrites(t *testing.T) {
	Convey("Given a live subtype under an archived parent", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		parent, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		child, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "widget", DisplayName: "Widget", ExtendsID: parent.ID.String(),
		})
		So(err, ShouldBeNil)
		sku, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: child.ID.String(), InternalName: "sku",
			DisplayName: "SKU", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).TypeDefinitions().Archive(ctx, parent.ID.String())
		So(err, ShouldBeNil)

		Convey("When a value is written under the live subtype", func() {
			_, werr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: sku.ID.String(), EntityID: "w1",
				TypeDefinitionID: child.ID.String(), Value: json.RawMessage(`"abc"`),
			})

			Convey("Then it is refused: the FQL binder would not find the data either", func() {
				So(werr, ShouldNotBeNil)
				So(domainerrors.IsArchived(werr), ShouldBeTrue)
			})
		})
	})
}

// TestRestoringAnAttributeRechecksItsName covers the collision a restore could
// re-introduce.
func TestRestoringAnAttributeRechecksItsName(t *testing.T) {
	Convey("Given a name archived on one subtype and created on a sibling", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		root, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		a, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "widget", DisplayName: "Widget", ExtendsID: root.ID.String(),
		})
		So(err, ShouldBeNil)
		b, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "gadget", DisplayName: "Gadget", ExtendsID: root.ID.String(),
		})
		So(err, ShouldBeNil)

		onA, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: a.ID.String(), InternalName: "release_code",
			DisplayName: "Release code", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Archive(ctx, onA.ID.String())
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: b.ID.String(), InternalName: "release_code",
			DisplayName: "Release code", DataType: "string",
		})
		So(err, ShouldBeNil)

		Convey("When the archived one is restored", func() {
			_, rerr := svc.Interactors(ctx).Attributes().Restore(ctx, onA.ID.String())

			Convey("Then it is refused rather than leaving two live with one name", func() {
				So(domainerrors.IsConflict(rerr), ShouldBeTrue)
			})
		})
	})

	Convey("Given an archived attribute whose name is free", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		attr, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "code",
			DisplayName: "Code", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Archive(ctx, attr.ID.String())
		So(err, ShouldBeNil)

		Convey("When it is restored", func() {
			got, rerr := svc.Interactors(ctx).Attributes().Restore(ctx, attr.ID.String())

			Convey("Then it comes back", func() {
				So(rerr, ShouldBeNil)
				So(got.ArchivedAt, ShouldBeNil)
			})
		})
	})
}

// TestUnitFamilyIsStructural covers the flag the structural guard did not
// cover: stored quantities hold a magnitude in the CURRENT family's base
// unit, so changing the family reinterprets every one of them.
func TestUnitFamilyIsStructural(t *testing.T) {
	Convey("Given a quantity attribute in the mass family with stored values", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		mass, err := it.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g",
			Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)
		length, err := svc.Interactors(ctx).Units().Create(ctx, appunit.CreateInput{
			Name: "length", BaseUnit: "m", Units: map[string]float64{"m": 1},
		})
		So(err, ShouldBeNil)

		product, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		weight, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "weight",
			DisplayName: "Weight", DataType: "quantity", UnitFamilyID: mass.ID.String(),
		})
		So(err, ShouldBeNil)

		Convey("When the family is changed with values stored", func() {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(),
				Value:            json.RawMessage(`{"magnitude":"2","unit":"kg"}`),
			})
			So(serr, ShouldBeNil)

			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: weight.ID.String(), DisplayName: "Weight",
				UnitFamilyID: length.ID.String(),
			})

			Convey("Then it is refused: 2000 g would read as 2000 m", func() {
				So(domainerrors.IsConflict(uerr), ShouldBeTrue)
				So(uerr.Error(), ShouldContainSubstring, "unit family")
			})
		})

		Convey("When the family is changed with no values stored", func() {
			_, uerr := svc.Interactors(ctx).Attributes().Update(ctx, appattribute.UpdateInput{
				ID: weight.ID.String(), DisplayName: "Weight",
				UnitFamilyID: length.ID.String(),
			})

			Convey("Then it is allowed: nothing would be reinterpreted", func() {
				So(uerr, ShouldBeNil)
			})
		})
	})
}

// TestDependencyEffectRebasesOneOf covers the constraint arm the effect loop
// was missing.
//
// The attribute-level normaliser rebases a one_of's quantity members into the
// family's base unit; the dependency-effect loop rebased min and max but not
// one_of, so an effect naming an allowed quantity compared a caller's base
// magnitude against an unconverted member. The target became unwritable, and
// the runtime error blamed the writer for a rule the schema author wrote.
func TestDependencyEffectRebasesOneOf(t *testing.T) {
	Convey("Given a quantity attribute restricted by a dependency to 5 kg", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		mass, err := it.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)
		product, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx,
			apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		kind, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "kind",
			DisplayName: "Kind", DataType: "string",
		})
		So(err, ShouldBeNil)
		weight, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "weight",
			DisplayName: "Weight", DataType: "quantity", UnitFamilyID: mass.ID.String(),
		})
		So(err, ShouldBeNil)

		_, err = svc.Interactors(ctx).Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: kind.ID.String(),
			TargetAttributeID: weight.ID.String(),
			Conditions: json.RawMessage(`[{"kind":"equals",` +
				`"value":{"type":"string","value":"bagged"}}]`),
			Effect: json.RawMessage(`{"constraints":[{"kind":"one_of","values":[` +
				`{"type":"quantity","value":{"magnitude":"5","unit":"kg"}}]}]}`),
		})
		So(err, ShouldBeNil)

		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: kind.ID.String(), EntityID: "p1",
			TypeDefinitionID: product.ID.String(), Value: json.RawMessage(`"bagged"`),
		})
		So(err, ShouldBeNil)

		Convey("When the exact allowed quantity is written", func() {
			_, werr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(),
				Value:            json.RawMessage(`{"magnitude":"5","unit":"kg"}`),
			})

			Convey("Then it is accepted, whichever unit expresses it", func() {
				So(werr, ShouldBeNil)
			})
		})

		Convey("When the same magnitude is written in the base unit", func() {
			_, werr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(),
				Value:            json.RawMessage(`{"magnitude":"5000","unit":"g"}`),
			})

			Convey("Then it is accepted too: the comparison is on the base magnitude", func() {
				So(werr, ShouldBeNil)
			})
		})

		Convey("When a different quantity is written", func() {
			_, werr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
				TypeDefinitionID: product.ID.String(),
				Value:            json.RawMessage(`{"magnitude":"6","unit":"kg"}`),
			})

			Convey("Then it is refused, which is what the rule says", func() {
				So(werr, ShouldNotBeNil)
			})
		})
	})
}
