package memory_test

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	"github.com/zkrebbekx/flexitype/application/gql"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
)

// gqlData navigates a GraphQL result's data as nested maps/slices.
func gqlData(res interface{}) map[string]interface{} {
	m, ok := res.(map[string]interface{})
	So(ok, ShouldBeTrue)
	return m
}

func TestGraphQLReadAPI(t *testing.T) {
	Convey("Given a product/supplier schema with values and a supplied_by relationship", t, func() {
		ctx := uow.WithTenant(context.Background(), valueobjects.DefaultTenant)
		svc := flexitype.NewInMemory()
		it := svc.Interactors(ctx)

		product, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "product", DisplayName: "Product"})
		So(err, ShouldBeNil)
		supplier, err := it.TypeDefinitions().Create(ctx, apptypedef.CreateInput{InternalName: "supplier", DisplayName: "Supplier"})
		So(err, ShouldBeNil)

		mk := func(typeID, name, dt string) string {
			a, e := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: typeID, InternalName: name, DisplayName: name, DataType: dt,
			})
			So(e, ShouldBeNil)
			return a.ID.String()
		}
		nameA := mk(product.ID.String(), "name", "string")
		priceA := mk(product.ID.String(), "price", "decimal")
		regionA := mk(supplier.ID.String(), "region", "string")

		def, err := it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "supplied_by", DisplayName: "Supplied by",
			ParentTypeID: product.ID.String(), ChildTypeID: supplier.ID.String(),
			ChildLabel: "suppliers", ParentLabel: "products",
		})
		So(err, ShouldBeNil)
		// A symmetric self-relation on product, for the depth-limit test.
		_, err = it.Relationships().CreateDefinition(ctx, apprelationship.CreateDefinitionInput{
			InternalName: "similar", DisplayName: "Similar", Kind: "symmetric",
			ParentTypeID: product.ID.String(), ChildTypeID: product.ID.String(),
		})
		So(err, ShouldBeNil)

		set := func(typeID, entity, attrID, raw string) {
			_, e := it.Values().Set(ctx, appvalue.SetInput{
				AttributeDefinitionID: attrID, EntityID: entity, TypeDefinitionID: typeID,
				Value: json.RawMessage(raw),
			})
			So(e, ShouldBeNil)
		}
		set(product.ID.String(), "prod1", priceA, `"150"`)
		set(product.ID.String(), "prod1", nameA, `"Widget"`)
		set(product.ID.String(), "prod2", priceA, `"50"`)
		set(supplier.ID.String(), "sup1", regionA, `"EU"`)

		_, err = it.Relationships().Link(ctx, apprelationship.LinkInput{
			DefinitionID: def.ID.String(), ParentEntity: "prod1", ChildEntity: "sup1",
		})
		So(err, ShouldBeNil)

		eng := svc.GraphQLEngine()
		// Mirror the HTTP middleware: install a request-scoped interactor set on
		// the context before executing.
		exec := func(c context.Context, query string) *gql.Result {
			return eng.Execute(application.WithInteractors(c, svc.Interactors(c)), query, nil)
		}

		Convey("When querying products filtered by FQL with nested suppliers", func() {
			res := exec(ctx, `{ product(filter:"price > 100") { entityId name suppliers { entityId region } } }`)

			Convey("Then only the matching product is returned, with its nested supplier", func() {
				So(res.Errors, ShouldBeEmpty)
				data := gqlData(res.Data)
				products, ok := data["product"].([]interface{})
				So(ok, ShouldBeTrue)
				So(len(products), ShouldEqual, 1)
				p0 := products[0].(map[string]interface{})
				So(p0["entityId"], ShouldEqual, "prod1")
				So(p0["name"], ShouldEqual, "Widget")
				suppliers := p0["suppliers"].([]interface{})
				So(len(suppliers), ShouldEqual, 1)
				So(suppliers[0].(map[string]interface{})["region"], ShouldEqual, "EU")
			})
		})

		Convey("When a query nests beyond the depth limit", func() {
			res := exec(ctx, `{ product { similar { similar { similar { similar { similar { similar { entityId } } } } } } } }`)

			Convey("Then it is rejected", func() {
				So(res.Errors, ShouldNotBeEmpty)
			})
		})

		Convey("When an attribute is added after first use", func() {
			// First use builds and caches the schema.
			_ = exec(ctx, `{ product { entityId } }`)
			// A query for a not-yet-existing field fails.
			before := exec(ctx, `{ product { sku } }`)
			So(before.Errors, ShouldNotBeEmpty)

			_, err := it.Attributes().Create(ctx, appattribute.CreateInput{
				TypeDefinitionID: product.ID.String(), InternalName: "sku", DisplayName: "SKU", DataType: "string",
			})
			So(err, ShouldBeNil)

			Convey("Then the schema regenerates within the same definition-event cycle", func() {
				after := exec(ctx, `{ product { sku } }`)
				So(after.Errors, ShouldBeEmpty)
			})
		})

		Convey("When introspecting the schema", func() {
			res := exec(ctx, `{ __schema { queryType { fields { name } } } }`)

			Convey("Then it reflects only this tenant's types", func() {
				So(res.Errors, ShouldBeEmpty)
				data := gqlData(res.Data)
				schema := data["__schema"].(map[string]interface{})
				qt := schema["queryType"].(map[string]interface{})
				fields := qt["fields"].([]interface{})
				names := map[string]bool{}
				for _, f := range fields {
					names[f.(map[string]interface{})["name"].(string)] = true
				}
				So(names["product"], ShouldBeTrue)
				So(names["supplier"], ShouldBeTrue)
				So(names["gadget"], ShouldBeFalse)
			})
		})

		Convey("When a second tenant introspects the same engine", func() {
			ctx2 := uow.WithTenant(context.Background(), valueobjects.TenantID("other"))
			_, err := svc.Interactors(ctx2).TypeDefinitions().Create(ctx2, apptypedef.CreateInput{InternalName: "gadget", DisplayName: "Gadget"})
			So(err, ShouldBeNil)
			res := exec(ctx2, `{ __schema { queryType { fields { name } } } }`)

			Convey("Then it sees only its own type, not the first tenant's", func() {
				So(res.Errors, ShouldBeEmpty)
				data := gqlData(res.Data)
				schema := data["__schema"].(map[string]interface{})
				qt := schema["queryType"].(map[string]interface{})
				fields := qt["fields"].([]interface{})
				names := map[string]bool{}
				for _, f := range fields {
					names[f.(map[string]interface{})["name"].(string)] = true
				}
				So(names["gadget"], ShouldBeTrue)
				So(names["product"], ShouldBeFalse)
			})
		})
	})
}
