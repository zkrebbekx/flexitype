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
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

func TestCompleteness(t *testing.T) {
	Convey("Given a product type with two required and two optional attributes", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{
			InternalName: "product", DisplayName: "Product",
		})
		So(err, ShouldBeNil)
		typeID := product.ID.String()

		attr := func(name string, required bool) string {
			snap, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name,
				DataType: "string", Required: required,
			})
			So(e, ShouldBeNil)
			return snap.ID.String()
		}
		sku := attr("sku", true)
		name := attr("name", true)
		category := attr("category", false)
		warranty := attr("warranty", false)

		setVal := func(attrID, entityID, v string) {
			raw, e := json.Marshal(v)
			So(e, ShouldBeNil)
			_, e = it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entityID,
				TypeDefinitionID: typeID, Value: raw,
			})
			So(e, ShouldBeNil)
		}

		// A dependency makes warranty required when category is "electronics".
		_, err = it.Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: category, TargetAttributeID: warranty,
			Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"string","value":"electronics"}}]`),
			Effect:     json.RawMessage(`{"required":true}`),
		})
		So(err, ShouldBeNil)

		Convey("When an entity fills one of two required attributes and no dependency fires", func() {
			setVal(sku, "p1", "ABC")
			setVal(category, "p1", "furniture")
			out, err := it.Dependencies().Completeness(ctx, typeID, "p1")

			Convey("Then it scores half, counting only base required attributes", func() {
				So(err, ShouldBeNil)
				So(out.Required, ShouldEqual, 2)
				So(out.Filled, ShouldEqual, 1)
				So(out.Score, ShouldAlmostEqual, 0.5)
				So(out.Missing, ShouldHaveLength, 1)
				So(out.Missing[0].InternalName, ShouldEqual, "name")
			})
		})

		Convey("When a dependency makes an optional attribute required", func() {
			setVal(sku, "p2", "ABC")
			setVal(name, "p2", "Widget")
			setVal(category, "p2", "electronics")
			out, err := it.Dependencies().Completeness(ctx, typeID, "p2")

			Convey("Then the required denominator grows and warranty is reported missing", func() {
				So(err, ShouldBeNil)
				So(out.Required, ShouldEqual, 3)
				So(out.Filled, ShouldEqual, 2)
				So(out.Score, ShouldAlmostEqual, 2.0/3.0)
				So(out.Missing, ShouldHaveLength, 1)
				So(out.Missing[0].InternalName, ShouldEqual, "warranty")
			})
		})

		Convey("When the type is scored in aggregate over both entities", func() {
			setVal(sku, "p1", "ABC")
			setVal(category, "p1", "furniture")
			setVal(sku, "p2", "ABC")
			setVal(name, "p2", "Widget")
			setVal(category, "p2", "electronics")
			out, err := it.Dependencies().TypeCompleteness(ctx, typeID)

			Convey("Then it averages the per-entity scores and counts none complete", func() {
				So(err, ShouldBeNil)
				So(out.Count, ShouldEqual, 2)
				So(out.Scored, ShouldEqual, 2)
				So(out.Complete, ShouldEqual, 0)
				So(out.Incomplete, ShouldEqual, 2)
				So(out.AverageScore, ShouldAlmostEqual, (0.5+2.0/3.0)/2.0)
			})
		})
	})
}
