package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	appschema "github.com/zkrebbekx/flexitype/application/schema"
	"github.com/zkrebbekx/flexitype/application/schema/templates"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

func intPtr(n int) *int { return &n }

// TestSchemaBundleCarriesNewFeatures proves the portable bundle round-trips
// the features shipped after #32: unit families, quantity attributes with a
// family reference, computed attributes, localizable flags and relationship
// cardinality.
func TestSchemaBundleCarriesNewFeatures(t *testing.T) {
	Convey("Given a schema using units, a computed attr, a localizable attr and cardinality", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		src := flexitype.NewInMemory()
		it := src.Interactors(ctx)

		fam, err := it.Units().Create(ctx, appunit.CreateInput{
			Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
		})
		So(err, ShouldBeNil)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		supplier, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "supplier", DisplayName: "Supplier"})
		So(err, ShouldBeNil)

		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "name", DisplayName: "Name",
			DataType: "string", Localizable: true,
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "price", DisplayName: "Price", DataType: "decimal",
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "cost", DisplayName: "Cost", DataType: "decimal",
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "margin", DisplayName: "Margin", DataType: "decimal",
			Computed: json.RawMessage(`{"kind":"formula","formula":"(price - cost) / price"}`),
		})
		So(err, ShouldBeNil)
		_, err = it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: product.ID.String(), InternalName: "weight", DisplayName: "Weight",
			DataType: "quantity", UnitFamilyID: fam.ID.String(), DisplayUnit: "g",
			Constraints: json.RawMessage(`[{"kind":"min_value","value":{"type":"quantity","value":{"magnitude":"1","unit":"g"}}}]`),
		})
		So(err, ShouldBeNil)
		_, err = it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "supplied_by", DisplayName: "Supplied by",
			ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
			MaxChildren: intPtr(5),
		})
		So(err, ShouldBeNil)

		bundle, err := it.Schema().Export(ctx)
		So(err, ShouldBeNil)

		Convey("When exported and imported into a fresh instance", func() {
			dst := flexitype.NewInMemory()
			res, err := dst.Interactors(ctx).Schema().Import(ctx, bundle)
			So(err, ShouldBeNil)

			Convey("Then the unit family, computed, localizable and cardinality all survive a re-export", func() {
				So(res.Attributes.Created, ShouldEqual, 5)
				So(len(bundle.UnitFamilies), ShouldEqual, 1)

				reExport, err := dst.Interactors(ctx).Schema().Export(ctx)
				So(err, ShouldBeNil)
				orig, _ := json.Marshal(bundle)
				round, _ := json.Marshal(reExport)
				So(string(round), ShouldEqual, string(orig))
			})
		})
	})
}

// TestTemplatesApply loads every embedded template into an empty tenant and
// asserts it yields a working, queryable schema.
func TestTemplatesApply(t *testing.T) {
	Convey("Given the embedded templates", t, func() {
		list := templates.List()
		So(len(list), ShouldBeGreaterThanOrEqualTo, 2)

		Convey("When each template is applied to an empty tenant", func() {
			ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
			for _, summary := range list {
				tmpl, ok := templates.Get(summary.Name)
				So(ok, ShouldBeTrue)
				svc := flexitype.NewInMemory()
				res, err := svc.Interactors(ctx).Schema().Import(ctx, tmpl.Bundle)
				So(err, ShouldBeNil)
				So(res.Types.Created, ShouldBeGreaterThan, 0)
				So(res.Attributes.Created, ShouldBeGreaterThan, 0)
			}
		})

		Convey("When the product-catalog template is applied and used end to end", func() {
			ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
			svc := flexitype.NewInMemory()
			tmpl, _ := templates.Get("product-catalog")
			_, err := svc.Interactors(ctx).Schema().Import(ctx, tmpl.Bundle)
			So(err, ShouldBeNil)

			it := svc.Interactors(ctx)
			pt, err := it.TypeDefinitions().GetByInternalName(ctx, "product")
			So(err, ShouldBeNil)
			entity := ulid.New().String()

			set := func(attr string, raw string) error {
				a, aerr := it.Attributes().List(ctx, appattribute.ListInput{
					TypeDefinitionID: pt.ID.String(), InternalNames: []string{attr}, Page: db.PageArgs{Limit: intPtr(1)},
				})
				So(aerr, ShouldBeNil)
				So(len(a.Items), ShouldEqual, 1)
				_, serr := it.Values().Set(ctx, appvalue.SetInput{
					AttributeDefinitionID: a.Items[0].ID.String(),
					EntityID:              entity,
					TypeDefinitionID:      pt.ID.String(),
					Value:                 json.RawMessage(raw),
				})
				return serr
			}

			Convey("Then a quantity value normalizes and FQL finds it, and margin computes", func() {
				So(set("price", `"100"`), ShouldBeNil)
				So(set("cost", `"40"`), ShouldBeNil)
				So(set("weight", `{"magnitude":"2","unit":"kg"}`), ShouldBeNil)

				q := svc.Interactors(ctx).Query()
				out, err := q.Execute(ctx, appquery.ExecuteInput{Type: "product", Query: "weight > 1500 g"})
				So(err, ShouldBeNil)
				So(len(out.Items), ShouldEqual, 1)

				out2, err := q.Execute(ctx, appquery.ExecuteInput{Type: "product", Query: "margin > 0.5"})
				So(err, ShouldBeNil)
				So(len(out2.Items), ShouldEqual, 1)
			})

			Convey("And the quantity min-value constraint (carried by the template) rejects a sub-gram weight", func() {
				So(set("weight", `{"magnitude":"0","unit":"g"}`), ShouldNotBeNil)
			})
		})
	})
}

// TestTypeClone proves a cloned type is an independent copy.
func TestTypeClone(t *testing.T) {
	Convey("Given a type with attributes and an intra-type dependency", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		src, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "widget", DisplayName: "Widget"})
		So(err, ShouldBeNil)
		flag, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: src.ID.String(), InternalName: "flagged", DisplayName: "Flagged", DataType: "bool",
		})
		So(err, ShouldBeNil)
		code, err := it.Attributes().Create(ctx, appattribute.CreateInput{
			TypeDefinitionID: src.ID.String(), InternalName: "code", DisplayName: "Code", DataType: "string",
		})
		So(err, ShouldBeNil)
		_, err = it.Dependencies().Create(ctx, appdependency.CreateInput{
			SourceAttributeID: flag.ID.String(), TargetAttributeID: code.ID.String(),
			Conditions: json.RawMessage(`[{"kind":"equals","value":{"type":"bool","value":true}}]`),
			Effect:     json.RawMessage(`{"required":true}`),
		})
		So(err, ShouldBeNil)

		Convey("When it is cloned", func() {
			res, err := svc.Interactors(ctx).Schema().Clone(ctx, appschema.CloneInput{
				SourceTypeID: src.ID.String(), InternalName: "widget_copy", DisplayName: "Widget Copy",
			})
			So(err, ShouldBeNil)

			Convey("Then attributes and the dependency are copied, as a fresh root", func() {
				So(res.Attributes, ShouldEqual, 2)
				So(res.Dependencies, ShouldEqual, 1)
				So(res.Type.ExtendsID, ShouldBeNil)
			})

			Convey("And mutating the clone leaves the original untouched", func() {
				clone := res.Type
				_, err := svc.Interactors(ctx).Attributes().Create(ctx, appattribute.CreateInput{
					TypeDefinitionID: clone.ID.String(), InternalName: "extra", DisplayName: "Extra", DataType: "string",
				})
				So(err, ShouldBeNil)

				origAttrs, err := svc.Interactors(ctx).Attributes().ListByTypeDefinition(ctx, src.ID.String(), db.PageArgs{Limit: intPtr(50)})
				So(err, ShouldBeNil)
				So(len(origAttrs.Items), ShouldEqual, 2) // unchanged
			})

			Convey("And an internal-name collision is rejected", func() {
				_, err := svc.Interactors(ctx).Schema().Clone(ctx, appschema.CloneInput{
					SourceTypeID: src.ID.String(), InternalName: "widget", DisplayName: "Dup",
				})
				So(err, ShouldNotBeNil)
			})
		})
	})
}
