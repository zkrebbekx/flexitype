package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
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
	})
}
