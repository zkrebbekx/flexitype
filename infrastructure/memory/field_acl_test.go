package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestFieldLevelAccess(t *testing.T) {
	Convey("Given a product with sku and a restricted cost attribute", t, func() {
		admin := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		ia := svc.Interactors(admin)
		product, err := ia.TypeDefinitions().Create(admin, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		typeID := product.ID.String()
		sku, err := ia.Attributes().Create(admin, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: "sku", DisplayName: "SKU", DataType: "string"})
		So(err, ShouldBeNil)
		cost, err := ia.Attributes().Create(admin, appattribute.CreateInput{TypeDefinitionID: typeID, InternalName: "cost", DisplayName: "Cost", DataType: "float"})
		So(err, ShouldBeNil)

		// Admin seeds both values.
		set := func(ctx context.Context, attrID string, v any) error {
			raw, _ := json.Marshal(v)
			_, e := svc.Interactors(ctx).Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: "p1", TypeDefinitionID: typeID, Value: raw,
			})
			return e
		}
		So(set(admin, sku.ID.String(), "ABC"), ShouldBeNil)
		So(set(admin, cost.ID.String(), 250.0), ShouldBeNil)

		readonlyCost := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{"cost": uow.PermRead}})
		noCost := uow.WithAccess(admin, uow.Access{Attr: map[string]uow.Perm{"cost": uow.PermNone}})

		Convey("A read-only-on-cost principal reads cost but cannot write it", func() {
			vals, err := svc.Interactors(readonlyCost).Values().ListByEntity(readonlyCost, typeID, "p1")
			So(err, ShouldBeNil)
			So(len(vals), ShouldEqual, 2) // sku + cost both visible

			err = set(readonlyCost, cost.ID.String(), 300.0)
			So(err, ShouldNotBeNil)
			So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)

			// sku (unrestricted) is still writable.
			So(set(readonlyCost, sku.ID.String(), "XYZ"), ShouldBeNil)
		})

		Convey("Field ACL applies to reading and deleting a value by id", func() {
			vals, err := ia.Values().ListByEntity(admin, typeID, "p1")
			So(err, ShouldBeNil)
			var costValueID string
			for _, v := range vals {
				if v.AttributeDefinitionID.String() == cost.ID.String() {
					costValueID = v.ID.String()
				}
			}
			So(costValueID, ShouldNotBeBlank)

			// none-on-cost: Get by id is invisible (NotFound), not leaked.
			_, err = svc.Interactors(noCost).Values().Get(noCost, costValueID)
			So(domainerrors.IsNotFound(err), ShouldBeTrue)

			// read-only-on-cost: delete is a write, so it is forbidden.
			_, err = svc.Interactors(readonlyCost).Values().Remove(readonlyCost, costValueID)
			So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeForbidden)

			// admin reads and deletes it.
			_, err = ia.Values().Get(admin, costValueID)
			So(err, ShouldBeNil)
			_, err = ia.Values().Remove(admin, costValueID)
			So(err, ShouldBeNil)
		})

		Convey("A none-on-cost principal cannot see or query cost", func() {
			vals, err := svc.Interactors(noCost).Values().ListByEntity(noCost, typeID, "p1")
			So(err, ShouldBeNil)
			So(len(vals), ShouldEqual, 1) // only sku

			eff, err := svc.Interactors(noCost).TypeDefinitions().EffectiveAttributes(noCost, typeID)
			So(err, ShouldBeNil)
			names := []string{}
			for _, e := range eff {
				names = append(names, e.Attribute.InternalName)
			}
			So(names, ShouldContain, "sku")
			So(names, ShouldNotContain, "cost")

			_, err = svc.Interactors(noCost).Query().Execute(noCost, appquery.ExecuteInput{Type: "product", Query: "cost > 100"})
			So(err, ShouldNotBeNil)
			So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation) // unknown attribute
		})

		Convey("The admin principal sees and queries everything", func() {
			vals, err := ia.Values().ListByEntity(admin, typeID, "p1")
			So(err, ShouldBeNil)
			So(len(vals), ShouldEqual, 2)
			out, err := ia.Query().Execute(admin, appquery.ExecuteInput{Type: "product", Query: "cost > 100"})
			So(err, ShouldBeNil)
			So(len(out.Items), ShouldEqual, 1)
		})

		Convey("A none-on-cost principal cannot leak cost via grid, facets, or export", func() {
			ic := svc.Interactors(noCost)

			Convey("Grid drops the restricted column and rejects it by name", func() {
				grid, err := ic.Values().GridRows(noCost, typeID, []string{"sku"}, []string{"p1"})
				So(err, ShouldBeNil)
				So(grid.Rows, ShouldHaveLength, 1)
				_, hasCost := grid.Rows[0].Values["cost"]
				So(hasCost, ShouldBeFalse)

				_, err = ic.Values().GridRows(noCost, typeID, []string{"sku", "cost"}, []string{"p1"})
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})

			Convey("Facets refuse to bucket the restricted attribute", func() {
				_, err := ic.Values().Facets(noCost, typeID, []string{"cost"}, []string{"p1"})
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})

			Convey("Export omits the restricted column by default and rejects it by name", func() {
				out, err := ic.Values().Export(noCost, appvalue.ExportInput{TypeDefinitionID: typeID})
				So(err, ShouldBeNil)
				So(out.Columns, ShouldContain, "sku")
				So(out.Columns, ShouldNotContain, "cost")

				_, err = ic.Values().Export(noCost, appvalue.ExportInput{TypeDefinitionID: typeID, Attributes: []string{"cost"}})
				So(err, ShouldNotBeNil)
				So(domainerrors.CodeOf(err), ShouldEqual, domainerrors.CodeValidation)
			})
		})

		Convey("The admin principal still sees cost in grid and export", func() {
			grid, err := ia.Values().GridRows(admin, typeID, []string{"sku", "cost"}, []string{"p1"})
			So(err, ShouldBeNil)
			So(grid.Rows[0].Values["cost"], ShouldEqual, "250")

			out, err := ia.Values().Export(admin, appvalue.ExportInput{TypeDefinitionID: typeID})
			So(err, ShouldBeNil)
			So(out.Columns, ShouldContain, "cost")
		})
	})
}

func TestChangeSetCannotCrossTenant(t *testing.T) {
	Convey("Given tenant A holds a value and tenant B runs a change-set", t, func() {
		svc := flexitype.NewInMemory()
		ctxA := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-a"))
		ctxB := uow.WithTenant(context.Background(), valueobjects.TenantID("tenant-b"))
		a := svc.Interactors(ctxA)

		product, err := a.TypeDefinitions().Create(ctxA, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		cost, err := a.Attributes().Create(ctxA, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "cost", DisplayName: "Cost", DataType: "float",
		})
		So(err, ShouldBeNil)
		_, err = a.Values().Set(ctxA, appvalue.SetInput{
			AttributeDefinitionID: cost.ID.String(), EntityID: "p1", TypeDefinitionID: product.ID.String(),
			Value: json.RawMessage(`250`),
		})
		So(err, ShouldBeNil)

		Convey("When tenant B publishes a change-set removing tenant A's value", func() {
			b := svc.Interactors(ctxB)
			cs, err := b.ChangeSets().Create(ctxB, appchangeset.CreateInput{Name: "cross-tenant"})
			So(err, ShouldBeNil)
			// tenant B references tenant A's attribute id (ids are not secrets).
			_, _ = b.ChangeSets().AddMutation(ctxB, cs.ID.String(), appvalue.Mutation{
				Kind: appvalue.MutationRemove, AttributeDefinitionID: cost.ID.String(), EntityID: "p1",
			})
			_, _ = b.ChangeSets().Submit(ctxB, cs.ID.String())
			_, _ = b.ChangeSets().Publish(ctxB, cs.ID.String())

			Convey("Then tenant A's value survives", func() {
				vals, err := a.Values().ListByEntity(ctxA, product.ID.String(), "p1")
				So(err, ShouldBeNil)
				So(len(vals), ShouldEqual, 1)
			})
		})
	})
}
