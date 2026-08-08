package flexitype_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// TestFQLQuantityLiteralErrors pins the messages the binder gives when a
// quantity comparison cannot be coerced.
//
// A quantity compares on its BASE magnitude, so the binder has to convert the
// literal before it can compile anything. Each way that conversion fails has
// its own message, and each message has to name what the author should write
// instead — an unusable comparison that reported "invalid query" would leave
// them guessing which half was wrong.
func TestFQLQuantityLiteralErrors(t *testing.T) {
	Convey("Given a product with a quantity attribute in a mass family", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		mass, err := ia.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)
		product, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		weight, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "weight", DisplayName: "Weight",
			DataType: "quantity", UnitFamilyID: mass.ID.String(), DisplayUnit: "g",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: weight.ID.String(), EntityID: "p1",
			TypeDefinitionID: product.ID.String(),
			Value:            json.RawMessage(`{"magnitude":"2","unit":"kg"}`),
		})
		So(err, ShouldBeNil)

		run := func(q string) ([]string, error) {
			out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "product", Query: q, Page: db.PageArgs{},
			})
			if qerr != nil {
				return nil, qerr
			}
			ids := make([]string, 0, len(out.Items))
			for _, r := range out.Items {
				ids = append(ids, r.EntityID)
			}
			return ids, nil
		}

		Convey("When the literal carries no unit", func() {
			_, err := run(`weight > 1000`)

			Convey("Then the message shows the family's BASE unit, not a placeholder", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "requires a unit")
				So(err.Error(), ShouldContainSubstring, "1000 g")
			})
		})

		Convey("When the literal is not a number", func() {
			_, err := run(`weight > "heavy"`)

			Convey("Then it says a number with a unit is expected", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "number with a unit")
			})
		})

		Convey("When the unit is not a member of the family", func() {
			_, err := run(`weight > 5 furlong`)

			Convey("Then it says the unit is outside the attribute's family", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "not part of the attribute's unit family")
			})
		})

		Convey("When a unit suffix is put on a non-quantity attribute", func() {
			_, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "price",
				DisplayName: "Price", DataType: "integer",
			})
			So(aerr, ShouldBeNil)
			_, err := run(`price > 5 kg`)

			Convey("Then it says the suffix is only valid on a quantity", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "only valid on quantity")
			})
		})

		Convey("When the comparison is well formed", func() {
			ids, err := run(`weight > 1 kg`)

			Convey("Then it compiles and matches on the base magnitude", func() {
				So(err, ShouldBeNil)
				So(ids, ShouldResemble, []string{"p1"})
			})
		})
	})
}

// TestFQLLinkAttributesThroughInheritance covers the binder's walk up a
// relationship definition's extends chain.
//
// A relationship definition can extend another, and the extending definition
// layers its attribute set on top of the base's. A query filtering on
// link.<name> has to see BOTH sets, or a link attribute declared once on the
// base becomes unqueryable on every definition that extends it.
func TestFQLLinkAttributesThroughInheritance(t *testing.T) {
	Convey("Given a relationship definition extending another", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		part, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "part", DisplayName: "Part",
		})
		So(err, ShouldBeNil)

		base, err := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "contains", DisplayName: "Contains",
			ParentTypeID: product.ID.String(), ChildTypeID: part.ID.String(),
		})
		So(err, ShouldBeNil)
		// A link attribute declared on the BASE definition's attribute set.
		baseSets, err := svc.Interactors(ctx).Relationships().AttributeSets(ctx, base.ID.String())
		So(err, ShouldBeNil)
		So(baseSets, ShouldNotBeEmpty)
		qty, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: baseSets[0], InternalName: "qty",
			DisplayName: "Qty", DataType: "integer",
		})
		So(err, ShouldBeNil)

		derived, err := svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "contains_critical", DisplayName: "Contains critical",
			ParentTypeID: product.ID.String(), ChildTypeID: part.ID.String(),
			ExtendsID: base.ID.String(),
		})
		So(err, ShouldBeNil)

		// Both endpoints need a live value: an entity with none is invisible
		// at the root and, since the counterpart-liveness guard, across a
		// traversal too.
		mkName := func(typeID, name string) string {
			a, aerr := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: "string",
			})
			So(aerr, ShouldBeNil)
			return a.ID.String()
		}
		pname := mkName(product.ID.String(), "name")
		cname := mkName(part.ID.String(), "code")
		for _, seed := range []struct{ attrID, entity, raw string }{
			{pname, "p1", `"P1"`}, {cname, "c1", `"C1"`},
		} {
			_, serr := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: seed.attrID, EntityID: seed.entity,
				Value: json.RawMessage(seed.raw),
			})
			So(serr, ShouldBeNil)
		}

		link, err := svc.Interactors(ctx).Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: derived.ID.String(), ParentEntity: "p1", ChildEntity: "c1",
		})
		So(err, ShouldBeNil)
		// A link attribute's values anchor to the LINK id.
		_, err = svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
			AttributeDefinitionID: qty.ID.String(), EntityID: link.ID.String(),
			Value: json.RawMessage(`7`),
		})
		So(err, ShouldBeNil)

		Convey("When a query filters on the INHERITED link attribute", func() {
			out, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "product", Query: `child(contains_critical){ link.qty = 7 }`, Page: db.PageArgs{},
			})

			Convey("Then it binds through the extends chain and matches", func() {
				So(qerr, ShouldBeNil)
				ids := []string{}
				for _, r := range out.Items {
					ids = append(ids, r.EntityID)
				}
				So(ids, ShouldResemble, []string{"p1"})
			})
		})
	})
}

// TestFQLTraversalErrors pins what a traversal says when it cannot bind.
//
// A traversal names a relationship and a side. Naming a relationship the root
// type does not take, or picking a side an unordered relationship does not
// have, has to say so — the alternative is an empty result that reads as "no
// matching data" when the query never described the schema at all.
func TestFQLTraversalErrors(t *testing.T) {
	Convey("Given a directed and a symmetric relationship", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		ia := svc.Interactors(ctx)

		product, err := ia.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		part, err := svc.Interactors(ctx).TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "part", DisplayName: "Part",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: part.ID.String(), InternalName: "code",
			DisplayName: "Code", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "contains", DisplayName: "Contains",
			ParentTypeID: product.ID.String(), ChildTypeID: part.ID.String(),
		})
		So(err, ShouldBeNil)
		_, err = svc.Interactors(ctx).Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "compatible_with", DisplayName: "Compatible with", Kind: "symmetric",
			ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
		})
		So(err, ShouldBeNil)

		run := func(q string) error {
			_, qerr := svc.Interactors(ctx).Query().Execute(ctx, appquery.ExecuteInput{
				Type: "product", Query: q, Page: db.PageArgs{},
			})
			return qerr
		}

		Convey("When the traversal names a relationship this type does not take", func() {
			err := run(`child(no_such_rel){ has(code) }`)

			Convey("Then it says the relationship is unknown for the type", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "unknown relationship")
			})
		})

		Convey("When a symmetric relationship is traversed with a side", func() {
			err := run(`child(compatible_with){ has(name) }`)

			Convey("Then it points at linked(), which is the unordered form", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "symmetric")
				So(err.Error(), ShouldContainSubstring, "linked(compatible_with)")
			})
		})

		Convey("When the traversal conditions on an attribute the counterpart lacks", func() {
			err := run(`child(contains){ has(no_such_attr) }`)

			Convey("Then it reports the unknown attribute, not an empty page", func() {
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
				So(err.Error(), ShouldContainSubstring, "unknown attribute")
			})
		})
	})
}
