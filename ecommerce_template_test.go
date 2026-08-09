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

			Convey("Then status=active makes the SKU required", func() {
				typeID := a.typeIDByName("product")
				statusID := a.attrIDByName(typeID, "status")
				skuID := a.attrIDByName(typeID, "sku")

				set := a.post("/api/v1/values", map[string]any{
					"type_definition_id": typeID, "entity_id": "p1",
					"attribute_definition_id": statusID, "value": "active",
				})
				So(set.Status, ShouldEqual, http.StatusOK)

				eff := a.get("/api/v1/entities/" + typeID + "/p1/attributes/" + skuID + "/effective-schema")
				So(eff.Status, ShouldEqual, http.StatusOK)
				So(eff.object(t)["required"], ShouldEqual, true)
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

// TestStrictEcommerceTemplate is the shipped demonstration of on_write.
//
// Every other template reports its requirements, which is the default and
// right for data assembled over time. This one is the other branch of that
// decision, applied to a condition that is a lifecycle state: a product cannot
// be set active without the fields that make it sellable.
func TestStrictEcommerceTemplate(t *testing.T) {
	Convey("Given the strict ecommerce template applied to a tenant", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		applied := a.post("/api/v1/schema/templates/ecommerce_strict/apply", nil)
		So(applied.Status, ShouldEqual, http.StatusOK)

		typeID := a.typeIDByName("product")
		statusID := a.attrIDByName(typeID, "status")
		skuID := a.attrIDByName(typeID, "sku")
		priceID := a.attrIDByName(typeID, "price")

		Convey("When a product is set active on its own", func() {
			refused := a.post("/api/v1/values", map[string]any{
				"type_definition_id": typeID, "entity_id": "p1",
				"attribute_definition_id": statusID, "value": "active",
			})

			Convey("Then the write is refused", func() {
				So(refused.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(string(refused.Body), ShouldContainSubstring, "sku")
			})

			Convey("Then nothing was stored", func() {
				values := a.get("/api/v1/entities/" + typeID + "/p1/values")
				So(values.Status, ShouldEqual, http.StatusOK)
				So(values.items(t), ShouldBeEmpty)
			})
		})

		Convey("When the state and what it demands arrive together", func() {
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

			Convey("Then it commits", func() {
				So(ok.Status, ShouldEqual, http.StatusOK)
			})

			Convey("Then the effective schema says the rule blocks", func() {
				eff := a.get("/api/v1/entities/" + typeID + "/p1/attributes/" + skuID + "/effective-schema")
				So(eff.Status, ShouldEqual, http.StatusOK)
				So(eff.object(t)["required_enforcement"], ShouldEqual, "on_write")
			})
		})

		Convey("When a draft product is built one field at a time", func() {
			// The mode blocks a LIFECYCLE state, not data entry. A product
			// that never claims to be active is unaffected, which is what
			// makes the strict template usable at all.
			for _, step := range []struct{ attr, value string }{
				{skuID, "SKU-2"}, {priceID, "12.00"}, {statusID, "draft"},
			} {
				resp := a.post("/api/v1/values", map[string]any{
					"type_definition_id": typeID, "entity_id": "p2",
					"attribute_definition_id": step.attr, "value": step.value,
				})
				So(resp.Status, ShouldEqual, http.StatusOK)
			}

			Convey("Then going active afterwards is accepted", func() {
				// The order a person works in anyway: fill it in, then publish.
				resp := a.post("/api/v1/values", map[string]any{
					"type_definition_id": typeID, "entity_id": "p2",
					"attribute_definition_id": statusID, "value": "active",
				})
				So(resp.Status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When the lenient template is applied instead", func() {
			b := newAPI(t, flexitype.APIConfig{})
			So(b.post("/api/v1/schema/templates/ecommerce/apply", nil).Status, ShouldEqual, http.StatusOK)
			lenientType := b.typeIDByName("product")
			lenientStatus := b.attrIDByName(lenientType, "status")

			Convey("Then the same write is accepted and the gap reported", func() {
				resp := b.post("/api/v1/values", map[string]any{
					"type_definition_id": lenientType, "entity_id": "p1",
					"attribute_definition_id": lenientStatus, "value": "active",
				})
				So(resp.Status, ShouldEqual, http.StatusOK)

				comp := b.get("/api/v1/entities/" + lenientType + "/p1/completeness")
				So(comp.Status, ShouldEqual, http.StatusOK)
				So(comp.object(t)["missing"], ShouldNotBeEmpty)
			})
		})
	})
}
