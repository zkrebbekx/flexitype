package flexitype_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	flexispec "github.com/zkrebbekx/flexitype/api"
)

// TestResponseContract drives real requests through the API handler and
// validates each answer against the committed OpenAPI document.
//
// The document was wrong where it was present, and each kind of wrongness
// broke a generated client differently. Three responses were typed as bare
// arrays where the server wraps in {"items":[...]}, so a generated client
// failed to unmarshal them outright. POST /values documented only 201 while
// answering 200, which a strict generator reports as a protocol violation.
// The DataType enum omitted media and quantity, so a generated client
// rejected those values before sending a request — making the features look
// absent rather than the spec look wrong. Together they are the set of
// failures that makes a team abandon codegen.
//
// TestOpenAPIRouteCoverage already proves every route is *mentioned*. This
// proves the descriptions are *true*, which is the part that had drifted.
func TestResponseContract(t *testing.T) {
	Convey("Given the live handler and the committed OpenAPI document", t, func() {
		router, doc := specRouter(t)
		a := newAPI(t, flexitype.APIConfig{})

		typeID := a.post("/api/v1/type-definitions", map[string]any{
			"internal_name": "product", "display_name": "Product",
		}).str(t, "id")
		attrID := a.post("/api/v1/attributes", map[string]any{
			"type_definition_id": typeID, "internal_name": "sku",
			"display_name": "SKU", "data_type": "string",
		}).str(t, "id")
		a.post("/api/v1/values", map[string]any{
			"attribute_definition_id": attrID, "entity_id": "p1",
			"type_definition_id": typeID, "value": "ABC",
		})

		// The relationship and dependency surfaces went undocumented for
		// every response, so seed enough to read them back for real.
		otherType := a.post("/api/v1/type-definitions", map[string]any{
			"internal_name": "supplier", "display_name": "Supplier",
		}).str(t, "id")
		statusID := a.post("/api/v1/attributes", map[string]any{
			"type_definition_id": typeID, "internal_name": "status",
			"display_name": "Status", "data_type": "string",
		}).str(t, "id")
		depID := a.post("/api/v1/dependencies", map[string]any{
			"source_attribute_id": statusID, "target_attribute_id": attrID,
			"conditions": []any{map[string]any{"kind": "equals",
				"value": map[string]any{"type": "string", "value": "active"}}},
			"effect": map[string]any{"required": true},
		}).str(t, "id")
		relDefID := a.post("/api/v1/relationship-definitions", map[string]any{
			"internal_name": "supplied_by", "display_name": "Supplied by",
			"parent_type_id": otherType, "child_type_id": typeID,
		}).str(t, "id")

		Convey("Then every documented read validates against its schema", func() {
			paths := []string{
				"/api/v1/features",
				"/api/v1/type-definitions",
				"/api/v1/type-definitions/" + typeID,
				"/api/v1/type-definitions/" + typeID + "/attributes",
				"/api/v1/type-definitions/" + typeID + "/effective-attributes",
				"/api/v1/type-definitions/" + typeID + "/children",
				"/api/v1/attributes",
				"/api/v1/attributes/" + attrID,
				"/api/v1/values",
				"/api/v1/values?total=true",
				"/api/v1/entities/" + typeID + "/p1/values",
				"/api/v1/dependencies",
				"/api/v1/dependencies/" + depID,
				"/api/v1/relationship-definitions",
				"/api/v1/relationship-definitions/" + relDefID,
				"/api/v1/relationship-definitions/" + relDefID + "/attribute-sets",
				"/api/v1/relationships",
				"/api/v1/entities/" + typeID + "/p1/relationships",
				"/api/v1/entities/" + typeID + "/p1/attributes/" + attrID + "/effective-schema",
				"/api/v1/saved-views",
				"/api/v1/schema/templates",
				"/api/v1/activity",
			}
			var failures []string
			for _, p := range paths {
				if err := validateResponse(router, a.srv, http.MethodGet, p, nil); err != nil {
					failures = append(failures, "GET "+p+": "+err.Error())
				}
			}
			sort.Strings(failures)
			So(strings.Join(failures, "\n"), ShouldEqual, "")
		})

		Convey("Then a write answers with the status the document declares", func() {
			// POST /values documented 201 and answered 200. Set is an upsert,
			// so 200 is the correct behaviour and the document was wrong.
			err := validateResponse(router, a.srv, http.MethodPost, "/api/v1/values", map[string]any{
				"attribute_definition_id": attrID, "entity_id": "p2",
				"type_definition_id": typeID, "value": "XYZ",
			})
			So(err, ShouldBeNil)
		})

		Convey("Then every data type the server supports is in the enum", func() {
			// media and quantity were missing, so a generated client could
			// not create attributes of either type at all.
			schema := doc.Components.Schemas["DataType"]
			So(schema, ShouldNotBeNil)
			declared := map[string]bool{}
			for _, v := range schema.Value.Enum {
				name, ok := v.(string)
				So(ok, ShouldBeTrue)
				declared[name] = true
			}
			for _, dt := range []string{"media", "quantity", "string", "decimal", "json"} {
				So(declared[dt], ShouldBeTrue)
			}
		})
	})
}

// specRouter loads the committed document and builds a router over it.
func specRouter(t *testing.T) (routers.Router, *openapi3.T) {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(flexispec.SpecYAML)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	// The document's paths are relative to /api/v1 (the route-coverage test
	// prefixes it by hand), and it declares no server, so name one here.
	doc.Servers = openapi3.Servers{{URL: "/api/v1"}}
	r, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("build spec router: %v", err)
	}
	return r, doc
}

// validateResponse issues one request against the running handler and
// validates the answer against the operation the spec matches for it.
func validateResponse(router routers.Router, srv *httptest.Server, method, path string, body map[string]any) error {
	resp, raw, err := doRequest(srv, method, path, body)
	if err != nil {
		return err
	}

	specReq, err := http.NewRequest(method, path, nil)
	if err != nil {
		return err
	}
	route, pathParams, err := router.FindRoute(specReq)
	if err != nil {
		return err
	}
	return openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    specReq,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			},
		},
		Status: resp.StatusCode,
		Header: resp.Header,
		Body:   io.NopCloser(bytes.NewReader(raw)),
		Options: &openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
	})
}

// doRequest issues one request against the running handler and returns the
// response with its body already read.
func doRequest(srv *httptest.Server, method, path string, body map[string]any) (*http.Response, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := jsonMarshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return resp, raw, err
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
