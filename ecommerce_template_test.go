package flexitype_test

import (
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
)

// TestEcommerceTemplate covers the curated "ecommerce" bundle that
// examples/marketplace applies into every merchant tenant.
//
// The templates package only proves a bundle PARSES. It never applies one, so
// a bundle that names an unknown data type, an ill-typed constraint operand or
// a dependency on a missing attribute still ships green and fails at the one
// moment it is used: the first merchant onboarding. This test applies the
// bundle through the real REST handler and reads the resulting schema back.
func TestEcommerceTemplate(t *testing.T) {
	Convey("Given a service with the curated templates", t, func() {
		a := newAPI(t, flexitype.APIConfig{})

		Convey("When the ecommerce template is applied", func() {
			resp := a.post("/api/v1/schema/templates/ecommerce/apply", nil)

			Convey("Then it imports", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
			})

			Convey("Then the root product type carries the commerce fields", func() {
				typeID := a.typeIDByName("product")
				So(typeID, ShouldNotBeEmpty)

				attrs := map[string]string{}
				for _, raw := range a.get("/api/v1/type-definitions/" + typeID + "/effective-attributes").items(t) {
					entry := raw.(map[string]any)["attribute"].(map[string]any)
					attrs[entry["internal_name"].(string)] = entry["data_type"].(string)
				}
				So(attrs, ShouldResemble, map[string]string{
					"name":        "string",
					"description": "text",
					"sku":         "string",
					"status":      "enum",
					"price":       "decimal",
					"currency":    "enum",
					"in_stock":    "bool",
					"image":       "media",
				})
			})

			Convey("Then a merchant can extend product with its own subtype", func() {
				parent := a.typeIDByName("product")
				sub := a.post("/api/v1/type-definitions", map[string]any{
					"internal_name": "apparel", "display_name": "Apparel", "extends_id": parent,
				})
				So(sub.Status, ShouldEqual, http.StatusCreated)

				Convey("And the subtype inherits every product field", func() {
					names := map[string]bool{}
					for _, raw := range a.get("/api/v1/type-definitions/" + sub.str(t, "id") + "/effective-attributes").items(t) {
						entry := raw.(map[string]any)["attribute"].(map[string]any)
						names[entry["internal_name"].(string)] = true
					}
					So(names["name"], ShouldBeTrue)
					So(names["price"], ShouldBeTrue)
					So(names["status"], ShouldBeTrue)
				})
			})

			Convey("Then a product cannot go active without a SKU", func() {
				// active is a LIFECYCLE state, so the template's rule blocks
				// the write rather than reporting the gap. A shopper-visible
				// product with no SKU is not a product that should exist.
				typeID := a.typeIDByName("product")
				statusID := a.attrIDByName(typeID, "status")
				skuID := a.attrIDByName(typeID, "sku")

				refused := a.post("/api/v1/values", map[string]any{
					"type_definition_id": typeID, "entity_id": "p1",
					"attribute_definition_id": statusID, "value": "active",
				})
				So(refused.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(string(refused.Body), ShouldContainSubstring, "sku")

				Convey("And both written together are accepted", func() {
					// The check runs at the end of the write, so one batch
					// carrying the state and what it demands passes.
					priceID := a.attrIDByName(typeID, "price")
					ok := a.post("/api/v1/values/batch", map[string]any{
						"items": []any{
							map[string]any{
								"type_definition_id": typeID, "entity_id": "p1",
								"attribute_definition_id": statusID, "value": "active",
							},
							map[string]any{
								"type_definition_id": typeID, "entity_id": "p1",
								"attribute_definition_id": skuID, "value": "SKU-1",
							},
							map[string]any{
								"type_definition_id": typeID, "entity_id": "p1",
								"attribute_definition_id": priceID, "value": "10.00",
							},
						},
					})
					So(ok.Status, ShouldEqual, http.StatusOK)

					eff := a.get("/api/v1/entities/" + typeID + "/p1/attributes/" + skuID + "/effective-schema")
					So(eff.Status, ShouldEqual, http.StatusOK)
					So(eff.object(t)["required"], ShouldEqual, true)
					So(eff.object(t)["required_enforcement"], ShouldEqual, "on_write")
				})

				Convey("And the SKU first, then the state, is accepted too", func() {
					// The order a person would work in anyway.
					priceID := a.attrIDByName(typeID, "price")
					So(a.post("/api/v1/values", map[string]any{
						"type_definition_id": typeID, "entity_id": "p2",
						"attribute_definition_id": skuID, "value": "SKU-2",
					}).Status, ShouldEqual, http.StatusOK)
					So(a.post("/api/v1/values", map[string]any{
						"type_definition_id": typeID, "entity_id": "p2",
						"attribute_definition_id": priceID, "value": "12.00",
					}).Status, ShouldEqual, http.StatusOK)
					So(a.post("/api/v1/values", map[string]any{
						"type_definition_id": typeID, "entity_id": "p2",
						"attribute_definition_id": statusID, "value": "active",
					}).Status, ShouldEqual, http.StatusOK)
				})
			})

			Convey("Then applying it a second time is a safe no-op", func() {
				again := a.post("/api/v1/schema/templates/ecommerce/apply", nil)
				So(again.Status, ShouldEqual, http.StatusOK)
				So(a.get("/api/v1/type-definitions?internal_name=product").items(t), ShouldHaveLength, 1)
			})
		})
	})
}

// typeIDByName resolves a type definition id from its internal name.
func (a *api) typeIDByName(internalName string) string {
	a.t.Helper()
	list := a.get("/api/v1/type-definitions?internal_name=" + internalName).items(a.t)
	if len(list) == 0 {
		return ""
	}
	id, _ := list[0].(map[string]any)["id"].(string)
	return id
}

// attrIDByName resolves an attribute id from a type's effective attributes,
// so an inherited attribute resolves as readily as an own one.
func (a *api) attrIDByName(typeID, internalName string) string {
	a.t.Helper()
	for _, raw := range a.get("/api/v1/type-definitions/" + typeID + "/effective-attributes").items(a.t) {
		entry := raw.(map[string]any)["attribute"].(map[string]any)
		if entry["internal_name"] == internalName {
			id, _ := entry["id"].(string)
			return id
		}
	}
	return ""
}
