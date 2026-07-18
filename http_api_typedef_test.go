package flexitype_test

import (
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
)

// TestHTTPTypeDefinitionRoutes exercises /api/v1/type-definitions end to end:
// the CRUD lifecycle, the archive/restore pair, the derived read endpoints and
// every error branch the handlers can take (malformed body, oversized body,
// validation, not-found, conflict).
func TestHTTPTypeDefinitionRoutes(t *testing.T) {
	Convey("Given the REST API over an in-memory service", t, func() {
		a := newAPI(t, flexitype.APIConfig{})

		Convey("When a type definition is created", func() {
			resp := a.post("/api/v1/type-definitions", map[string]any{
				"internal_name": "product", "display_name": "Product", "description": "Sellable goods",
			})

			Convey("Then it is 201 with the persisted snapshot", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
				obj := resp.object(t)
				So(obj["internal_name"], ShouldEqual, "product")
				So(obj["display_name"], ShouldEqual, "Product")
				So(obj["tenant_id"], ShouldEqual, "default")
				So(obj["version"], ShouldEqual, float64(1))
				So(obj["id"], ShouldNotBeNil)
			})

			Convey("And it is readable by id", func() {
				id := resp.str(t, "id")
				got := a.get("/api/v1/type-definitions/" + id)
				So(got.Status, ShouldEqual, http.StatusOK)
				So(got.str(t, "internal_name"), ShouldEqual, "product")
			})

			Convey("And re-creating the same internal name conflicts", func() {
				dup := a.post("/api/v1/type-definitions", map[string]any{
					"internal_name": "product", "display_name": "Product Again",
				})
				So(dup.Status, ShouldEqual, http.StatusConflict)
				So(dup.errorCode(), ShouldEqual, "CONFLICT")
			})
		})

		Convey("When the request body is malformed JSON", func() {
			resp := a.post("/api/v1/type-definitions", `{"display_name":`)

			Convey("Then it is 422 VALIDATION rather than a 500", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldEqual, "invalid request body")
			})
		})

		Convey("When the body carries an unknown field", func() {
			resp := a.post("/api/v1/type-definitions", `{"display_name":"X","surprise":1}`)

			Convey("Then strict decoding rejects it 422 (typos are not silently dropped)", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(string(resp.Body), ShouldContainSubstring, "unknown field")
			})
		})

		Convey("When the body exceeds the 4 MiB JSON cap", func() {
			huge := `{"display_name":"` + strings.Repeat("a", (4<<20)+1024) + `"}`
			resp := a.post("/api/v1/type-definitions", huge)

			Convey("Then it is refused 422 before the payload is decoded", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldEqual, "request body too large")
			})
		})

		Convey("When the internal name is invalid", func() {
			resp := a.post("/api/v1/type-definitions", map[string]any{
				"internal_name": "Not Snake Case", "display_name": "X",
			})

			Convey("Then it is 422 with the offending field in details", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(string(resp.Body), ShouldContainSubstring, "internal_name")
			})
		})

		Convey("When an unknown id is fetched", func() {
			resp := a.get("/api/v1/type-definitions/" + missingULID)

			Convey("Then it is 404 NOT_FOUND", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("Given an existing type definition", func() {
			id := a.mustCreateType("product", "Product")

			Convey("When it is updated", func() {
				resp := a.patch("/api/v1/type-definitions/"+id, map[string]any{
					"display_name": "Products", "description": "Renamed",
				})

				Convey("Then the new display name is returned and the version advances", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(resp.str(t, "display_name"), ShouldEqual, "Products")
					So(resp.object(t)["version"], ShouldEqual, float64(2))
				})
			})

			Convey("When it is archived and then restored", func() {
				archived := a.post("/api/v1/type-definitions/"+id+"/archive", nil)
				So(archived.Status, ShouldEqual, http.StatusOK)

				Convey("Then it drops out of the default list", func() {
					list := a.get("/api/v1/type-definitions")
					So(list.Status, ShouldEqual, http.StatusOK)
					So(len(list.items(t)), ShouldEqual, 0)
				})

				Convey("Then ?include_archived=true still shows it", func() {
					list := a.get("/api/v1/type-definitions?include_archived=true")
					So(len(list.items(t)), ShouldEqual, 1)
				})

				Convey("Then restoring brings it back to the live list", func() {
					restored := a.post("/api/v1/type-definitions/"+id+"/restore", nil)
					So(restored.Status, ShouldEqual, http.StatusOK)
					So(len(a.get("/api/v1/type-definitions").items(t)), ShouldEqual, 1)
				})
			})

			Convey("When its derived read endpoints are called", func() {
				attrID := a.mustCreateAttr(id, "name", "string", nil)

				Convey("Then /attributes lists the declared attributes", func() {
					resp := a.get("/api/v1/type-definitions/" + id + "/attributes")
					So(resp.Status, ShouldEqual, http.StatusOK)
					items := resp.items(t)
					So(len(items), ShouldEqual, 1)
					So(items[0].(map[string]any)["id"], ShouldEqual, attrID)
				})

				Convey("Then /effective-attributes resolves the inherited set", func() {
					resp := a.get("/api/v1/type-definitions/" + id + "/effective-attributes")
					So(resp.Status, ShouldEqual, http.StatusOK)
					items := resp.items(t)
					So(len(items), ShouldEqual, 1)
					first := items[0].(map[string]any)
					So(first["attribute"], ShouldNotBeNil)
					So(first["declared_in"].(map[string]any)["id"], ShouldEqual, id)
				})

				Convey("Then /children is an empty array, never null", func() {
					resp := a.get("/api/v1/type-definitions/" + id + "/children")
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(string(resp.Body), ShouldContainSubstring, `"items":[]`)
				})

				Convey("Then /completeness reports the type's fill rate", func() {
					resp := a.get("/api/v1/type-definitions/" + id + "/completeness")
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(resp.str(t, "type_definition_id"), ShouldEqual, id)
					So(resp.object(t)["count"], ShouldEqual, float64(0))
				})
			})

			Convey("When a subtype extends it", func() {
				child := a.post("/api/v1/type-definitions", map[string]any{
					"internal_name": "book", "display_name": "Book", "extends_id": id,
				})
				So(child.Status, ShouldEqual, http.StatusCreated)

				Convey("Then the parent's /children lists the subtype", func() {
					resp := a.get("/api/v1/type-definitions/" + id + "/children")
					items := resp.items(t)
					So(len(items), ShouldEqual, 1)
					So(items[0].(map[string]any)["internal_name"], ShouldEqual, "book")
				})
			})
		})

		Convey("Given several type definitions", func() {
			a.mustCreateType("alpha", "Alpha")
			a.mustCreateType("bravo", "Bravo")
			a.mustCreateType("charlie", "Charlie")

			Convey("When the list is paged with ?limit=", func() {
				resp := a.get("/api/v1/type-definitions?limit=2")

				Convey("Then only the page is returned, with a next cursor", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(len(resp.items(t)), ShouldEqual, 2)
					pi := resp.pageInfo(t)
					So(pi["has_next_page"], ShouldBeTrue)
					So(pi["next_cursor"], ShouldNotBeNil)
				})

				Convey("And following the cursor returns the remainder", func() {
					cursor := resp.pageInfo(t)["next_cursor"].(string)
					next := a.get("/api/v1/type-definitions?limit=2&cursor=" + cursor)
					So(next.Status, ShouldEqual, http.StatusOK)
					So(len(next.items(t)), ShouldEqual, 1)
					So(next.pageInfo(t)["has_next_page"], ShouldBeFalse)
				})
			})

			Convey("When ?total=true is passed", func() {
				resp := a.get("/api/v1/type-definitions?limit=2&total=true")

				Convey("Then page_info carries the full count, not just the page size", func() {
					So(resp.pageInfo(t)["total_count"], ShouldEqual, float64(3))
				})
			})

			Convey("When ?total is omitted", func() {
				resp := a.get("/api/v1/type-definitions?limit=2")

				Convey("Then the count is not computed (absent from page_info)", func() {
					_, present := resp.pageInfo(t)["total_count"]
					So(present, ShouldBeFalse)
				})
			})

			Convey("When ?limit= is not a number", func() {
				resp := a.get("/api/v1/type-definitions?limit=lots")

				Convey("Then it is rejected 422, not silently defaulted", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
					So(resp.errorMessage(), ShouldContainSubstring, "positive integer")
				})
			})

			Convey("When ?cursor= is not a valid keyset cursor", func() {
				resp := a.get("/api/v1/type-definitions?cursor=not-a-cursor")

				Convey("Then it is rejected 422", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
					So(resp.errorMessage(), ShouldContainSubstring, "cursor")
				})
			})

			Convey("When ?internal_name= filters by a comma-separated list", func() {
				resp := a.get("/api/v1/type-definitions?internal_name=alpha,charlie")

				Convey("Then only the named types come back", func() {
					items := resp.items(t)
					So(len(items), ShouldEqual, 2)
					var names []string
					for _, it := range items {
						names = append(names, it.(map[string]any)["internal_name"].(string))
					}
					So(names, ShouldContain, "alpha")
					So(names, ShouldContain, "charlie")
				})
			})
		})
	})
}

// TestHTTPAttributeRoutes exercises /api/v1/attributes: creation against a
// type, the update/archive/restore lifecycle, filtered listing and the
// value-validation endpoint that the console uses for live form feedback.
func TestHTTPAttributeRoutes(t *testing.T) {
	Convey("Given a type definition to hang attributes off", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		typeID := a.mustCreateType("product", "Product")

		Convey("When an attribute is created", func() {
			resp := a.post("/api/v1/attributes", map[string]any{
				"type_definition_id": typeID,
				"internal_name":      "sku",
				"display_name":       "SKU",
				"data_type":          "string",
				"required":           true,
				"unique":             true,
				"group":              "identity",
				"help_text":          "Stock keeping unit",
				"constraints":        []any{map[string]any{"kind": "max_length", "n": 32}},
			})

			Convey("Then it is 201 with every declared facet echoed back", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
				obj := resp.object(t)
				So(obj["internal_name"], ShouldEqual, "sku")
				So(obj["data_type"], ShouldEqual, "string")
				So(obj["required"], ShouldBeTrue)
				So(obj["unique"], ShouldBeTrue)
				So(obj["type_definition_id"], ShouldEqual, typeID)
			})

			Convey("And its constraint is enforced by /validate-value", func() {
				id := resp.str(t, "id")

				ok := a.post("/api/v1/attributes/"+id+"/validate-value", map[string]any{"value": "SHORT"})
				So(ok.Status, ShouldEqual, http.StatusOK)
				So(ok.object(t)["valid"], ShouldBeTrue)

				tooLong := a.post("/api/v1/attributes/"+id+"/validate-value",
					map[string]any{"value": strings.Repeat("x", 40)})
				So(tooLong.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(tooLong.errorCode(), ShouldEqual, "VALIDATION")
			})

			Convey("And a value of the wrong type is rejected by /validate-value", func() {
				id := resp.str(t, "id")
				wrong := a.post("/api/v1/attributes/"+id+"/validate-value", map[string]any{"value": 42})
				So(wrong.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(wrong.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When the data type is not a known one", func() {
			resp := a.post("/api/v1/attributes", map[string]any{
				"type_definition_id": typeID, "internal_name": "x", "display_name": "X", "data_type": "vibes",
			})

			Convey("Then it is 422 naming the unknown type", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "unknown data type")
			})
		})

		Convey("When the owning type does not exist", func() {
			resp := a.post("/api/v1/attributes", map[string]any{
				"type_definition_id": missingULID, "internal_name": "x", "display_name": "X", "data_type": "string",
			})

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("Given attributes of several data types", func() {
			nameID := a.mustCreateAttr(typeID, "name", "string", nil)
			a.mustCreateAttr(typeID, "price", "float", nil)
			a.mustCreateAttr(typeID, "in_stock", "bool", nil)

			Convey("When the attribute is fetched by id", func() {
				resp := a.get("/api/v1/attributes/" + nameID)
				Convey("Then the snapshot comes back 200", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(resp.str(t, "internal_name"), ShouldEqual, "name")
				})
			})

			Convey("When an unknown attribute id is fetched", func() {
				resp := a.get("/api/v1/attributes/" + missingULID)
				Convey("Then it is 404", func() {
					So(resp.Status, ShouldEqual, http.StatusNotFound)
					So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
				})
			})

			Convey("When the list is filtered by data type", func() {
				resp := a.get("/api/v1/attributes?data_type=float,bool")

				Convey("Then only attributes of those types are returned", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					items := resp.items(t)
					So(len(items), ShouldEqual, 2)
					for _, it := range items {
						So(it.(map[string]any)["data_type"], ShouldBeIn, "float", "bool")
					}
				})
			})

			Convey("When the list is filtered by type definition", func() {
				other := a.mustCreateType("supplier", "Supplier")
				a.mustCreateAttr(other, "contact", "email", nil)

				resp := a.get("/api/v1/attributes?type_definition_id=" + other)
				Convey("Then only that type's attributes come back", func() {
					items := resp.items(t)
					So(len(items), ShouldEqual, 1)
					So(items[0].(map[string]any)["internal_name"], ShouldEqual, "contact")
				})
			})

			Convey("When the list is filtered by internal name", func() {
				resp := a.get("/api/v1/attributes?internal_name=name")
				Convey("Then exactly the named attribute comes back", func() {
					So(len(resp.items(t)), ShouldEqual, 1)
				})
			})

			Convey("When an attribute is updated", func() {
				resp := a.patch("/api/v1/attributes/"+nameID, map[string]any{
					"display_name": "Product Name", "required": true, "sort_order": 5,
				})

				Convey("Then the change is applied and the version advances", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					obj := resp.object(t)
					So(obj["display_name"], ShouldEqual, "Product Name")
					So(obj["required"], ShouldBeTrue)
					So(obj["version"], ShouldEqual, float64(2))
				})
			})

			Convey("When an attribute is archived", func() {
				resp := a.post("/api/v1/attributes/"+nameID+"/archive", nil)
				So(resp.Status, ShouldEqual, http.StatusOK)

				Convey("Then it leaves the live list and returns with ?include_archived", func() {
					So(len(a.get("/api/v1/attributes?internal_name=name").items(t)), ShouldEqual, 0)
					So(len(a.get("/api/v1/attributes?internal_name=name&include_archived=true").items(t)), ShouldEqual, 1)
				})

				Convey("Then restoring makes it live again", func() {
					restored := a.post("/api/v1/attributes/"+nameID+"/restore", nil)
					So(restored.Status, ShouldEqual, http.StatusOK)
					So(len(a.get("/api/v1/attributes?internal_name=name").items(t)), ShouldEqual, 1)
				})
			})

			Convey("When /validate-value is called on an unknown attribute", func() {
				resp := a.post("/api/v1/attributes/"+missingULID+"/validate-value", map[string]any{"value": "x"})
				Convey("Then it is 404", func() {
					So(resp.Status, ShouldEqual, http.StatusNotFound)
					So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
				})
			})

			Convey("When /validate-value receives a malformed body", func() {
				resp := a.post("/api/v1/attributes/"+nameID+"/validate-value", `{"value":`)
				Convey("Then it is 422 VALIDATION", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
				})
			})

			Convey("When an update body is malformed", func() {
				resp := a.patch("/api/v1/attributes/"+nameID, `{`)
				Convey("Then it is 422 VALIDATION", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
				})
			})
		})
	})
}
