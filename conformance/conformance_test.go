package conformance_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/client"
)

// TestClientConformance drives the first-party Go client against a real
// standalone handler (the in-memory service exposed over httptest), so the
// client can never silently drift from the API.
func TestClientConformance(t *testing.T) {
	Convey("Given a standalone flexitype service and its Go client", t, func() {
		svc := flexitype.NewInMemory(flexitype.WithSearchIndex())
		ts := httptest.NewServer(svc.APIHandler(flexitype.APIConfig{}))
		Reset(ts.Close)

		c, err := client.New(ts.URL) // dev mode: auth disabled, no token
		So(err, ShouldBeNil)
		ctx := context.Background()

		Convey("When modelling a type, attributes and values", func() {
			product, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "product", DisplayName: "Product"})
			So(err, ShouldBeNil)
			So(product.ID, ShouldNotBeEmpty)

			price, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
				TypeDefinitionID: product.ID, InternalName: "price", DisplayName: "Price", DataType: "decimal",
			})
			So(err, ShouldBeNil)
			name, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
				TypeDefinitionID: product.ID, InternalName: "name", DisplayName: "Name", DataType: "string",
			})
			So(err, ShouldBeNil)

			_, err = c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: price.ID, EntityID: "sku-1", TypeDefinitionID: product.ID, Value: json.RawMessage(`"19.99"`),
			})
			So(err, ShouldBeNil)
			_, err = c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: name.ID, EntityID: "sku-1", TypeDefinitionID: product.ID, Value: json.RawMessage(`"Widget"`),
			})
			So(err, ShouldBeNil)

			Convey("Then it reads back through the typed surface", func() {
				got, err := c.Types().Get(ctx, product.ID)
				So(err, ShouldBeNil)
				So(got.InternalName, ShouldEqual, "product")

				effs, err := c.Types().EffectiveAttributes(ctx, product.ID)
				So(err, ShouldBeNil)
				So(len(effs), ShouldEqual, 2)

				vals, err := c.Entities().Values(ctx, product.ID, "sku-1")
				So(err, ShouldBeNil)
				So(len(vals), ShouldEqual, 2)
			})

			Convey("And an FQL query iterates the matching entities", func() {
				var ids []string
				for row, err := range c.Query(ctx, "product", `price > 10`) {
					So(err, ShouldBeNil)
					ids = append(ids, row.EntityID)
				}
				So(ids, ShouldResemble, []string{"sku-1"})
			})

			Convey("And GraphQL returns the same data", func() {
				var res struct {
					Product struct {
						Edges []struct {
							Node struct {
								EntityID string `json:"entityId"`
								Name     string `json:"name"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"product"`
				}
				err := c.GraphQL(ctx, `{ product(filter:"price > 10") { edges { node { entityId name } } } }`, nil, &res)
				So(err, ShouldBeNil)
				So(len(res.Product.Edges), ShouldEqual, 1)
				So(res.Product.Edges[0].Node.Name, ShouldEqual, "Widget")
			})
		})

		Convey("When a resource is missing", func() {
			_, err := c.Attributes().Get(ctx, "01KX9V0000000000000000000A") // valid ulid, absent
			Convey("Then the error is a typed NOT_FOUND matched by errors.Is", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, client.ErrNotFound), ShouldBeTrue)
			})
		})

		Convey("When a pagination cursor is malformed", func() {
			_, err := c.Types().List(ctx, client.ListTypesOptions{ListOptions: client.ListOptions{Cursor: "not-a-valid-cursor"}})
			Convey("Then the server's VALIDATION error surfaces as a typed error", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, client.ErrValidation), ShouldBeTrue)
			})
		})

		Convey("When paging a list and requesting the total", func() {
			for i := 0; i < 5; i++ {
				_, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "t" + string(rune('a'+i)), DisplayName: "T"})
				So(err, ShouldBeNil)
			}

			page, err := c.Types().List(ctx, client.ListTypesOptions{ListOptions: client.ListOptions{Limit: 2, Total: true}})
			So(err, ShouldBeNil)
			So(len(page.Items), ShouldEqual, 2)
			So(page.PageInfo.TotalCount, ShouldNotBeNil)
			So(*page.PageInfo.TotalCount, ShouldEqual, 5)

			Convey("Then the All iterator walks every page exactly once", func() {
				seen := map[string]bool{}
				count := 0
				for td, err := range c.Types().All(ctx, client.ListTypesOptions{ListOptions: client.ListOptions{Limit: 2}}) {
					So(err, ShouldBeNil)
					So(seen[td.ID], ShouldBeFalse)
					seen[td.ID] = true
					count++
				}
				So(count, ShouldEqual, 5)
			})
		})

		Convey("When using units of measure", func() {
			fam, err := c.UnitFamilies().Create(ctx, client.CreateUnitFamilyInput{
				Name: "mass", BaseUnit: "g", Units: map[string]float64{"g": 1, "kg": 1000},
			})
			So(err, ShouldBeNil)
			product, err := c.Types().Create(ctx, client.CreateTypeInput{InternalName: "product", DisplayName: "Product"})
			So(err, ShouldBeNil)
			weight, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
				TypeDefinitionID: product.ID, InternalName: "weight", DisplayName: "Weight",
				DataType: "quantity", UnitFamilyID: fam.ID, DisplayUnit: "g",
			})
			So(err, ShouldBeNil)
			_, err = c.Values().Set(ctx, client.SetValueInput{
				AttributeDefinitionID: weight.ID, EntityID: "w1", TypeDefinitionID: product.ID,
				Value: json.RawMessage(`{"magnitude":"5","unit":"kg"}`),
			})
			So(err, ShouldBeNil)

			Convey("Then FQL compares across units via the family", func() {
				page, err := c.QueryPage(ctx, "product", `weight > 1500 g`)
				So(err, ShouldBeNil)
				So(len(page.Items), ShouldEqual, 1)
				So(page.Items[0].EntityID, ShouldEqual, "w1")
			})
		})

		Convey("When bootstrapping a tenant from a template", func() {
			res, err := c.Schema().ApplyTemplate(ctx, "product-catalog")
			So(err, ShouldBeNil)
			Convey("Then the schema is created and templates list", func() {
				So(res.Types.Created, ShouldBeGreaterThan, 0)
				tmpls, err := c.Schema().Templates(ctx)
				So(err, ShouldBeNil)
				So(len(tmpls), ShouldBeGreaterThanOrEqualTo, 2)
			})
		})

		Convey("When reading features", func() {
			f, err := c.Features(ctx)
			So(err, ShouldBeNil)
			So(f.SearchIndex, ShouldBeTrue)
		})
	})
}
