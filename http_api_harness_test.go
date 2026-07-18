package flexitype_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// The tests in http_api_*_test.go drive the real REST handler
// (internal/interfaces/http) over an in-memory service: flexitype.NewInMemory
// -> Service.APIHandler -> httptest.NewServer. That is the whole production
// stack minus Postgres — chi routing, the auth/rate-limit/interactor
// middleware chain, JSON decoding, the domain-error-to-status mapping and the
// response shapes clients actually parse.

// apiResponse is one decoded HTTP exchange.
type apiResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// object decodes the body as a JSON object. Test failure (not assertion
// failure) when the body is not an object, since every assertion downstream
// would then be meaningless.
func (r apiResponse) object(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("decode object (status %d): %v; body=%s", r.Status, err, r.Body)
	}
	return out
}

// items returns the "items" array of a list response.
func (r apiResponse) items(t *testing.T) []any {
	t.Helper()
	obj := r.object(t)
	raw, ok := obj["items"]
	if !ok {
		t.Fatalf("response has no items key: %s", r.Body)
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("items is not an array (a nil slice marshalled to null?): %s", r.Body)
	}
	return arr
}

// pageInfo returns the "page_info" object of a paginated response.
func (r apiResponse) pageInfo(t *testing.T) map[string]any {
	t.Helper()
	obj := r.object(t)
	pi, ok := obj["page_info"].(map[string]any)
	if !ok {
		t.Fatalf("response has no page_info object: %s", r.Body)
	}
	return pi
}

// errorCode returns the stable machine error code of an error response, or ""
// when the body is not an API error. Every non-2xx response is expected to
// carry one, so asserting on it (rather than only the status) pins the
// contract clients switch on.
func (r apiResponse) errorCode() string {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(r.Body, &body) != nil {
		return ""
	}
	return body.Error.Code
}

// errorMessage returns the human-readable half of an API error.
func (r apiResponse) errorMessage() string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(r.Body, &body) != nil {
		return ""
	}
	return body.Error.Message
}

// str reads a top-level string field.
func (r apiResponse) str(t *testing.T, key string) string {
	t.Helper()
	v, _ := r.object(t)[key].(string)
	return v
}

// api is a client bound to one running handler.
type api struct {
	t     *testing.T
	srv   *httptest.Server
	token string
}

// newAPI boots an in-memory service behind the real API handler. Options
// configure the service (blob store, search index, disabled features); cfg
// configures the handler (accounts, rate limiter, provisioning).
func newAPI(t *testing.T, cfg flexitype.APIConfig, opts ...flexitype.Option) *api {
	t.Helper()
	if cfg.Logger == nil {
		// Errors only: the request logger would otherwise emit a JSON line
		// per request and bury the test output.
		cfg.Logger = logger.New(logger.Config{Level: "error"})
	}
	svc := flexitype.NewInMemory(opts...)
	srv := httptest.NewServer(svc.APIHandler(cfg))
	t.Cleanup(srv.Close)
	return &api{t: t, srv: srv}
}

// as returns a client that presents the given bearer token.
func (a *api) as(token string) *api {
	clone := *a
	clone.token = token
	return &clone
}

func (a *api) request(req *http.Request) apiResponse {
	a.t.Helper()
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.srv.Client().Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatalf("read body: %v", err)
	}
	return apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: body}
}

// do sends a JSON request. body may be nil (no payload), a string/[]byte
// (sent verbatim, for malformed-JSON cases) or any value to marshal.
func (a *api) do(method, path string, body any) apiResponse {
	a.t.Helper()
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case string:
		reader = bytes.NewReader([]byte(v))
	case []byte:
		reader = bytes.NewReader(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			a.t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.srv.URL+path, reader)
	if err != nil {
		a.t.Fatalf("build request: %v", err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return a.request(req)
}

func (a *api) get(path string) apiResponse          { return a.do(http.MethodGet, path, nil) }
func (a *api) post(path string, b any) apiResponse  { return a.do(http.MethodPost, path, b) }
func (a *api) patch(path string, b any) apiResponse { return a.do(http.MethodPatch, path, b) }
func (a *api) put(path string, b any) apiResponse   { return a.do(http.MethodPut, path, b) }
func (a *api) delete(path string) apiResponse       { return a.do(http.MethodDelete, path, nil) }

// upload posts a multipart form: one file part plus optional plain fields.
func (a *api) upload(path, fileField, filename string, content []byte, fields map[string]string) apiResponse {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			a.t.Fatalf("write field %s: %v", k, err)
		}
	}
	if fileField != "" {
		part, err := mw.CreateFormFile(fileField, filename)
		if err != nil {
			a.t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			a.t.Fatalf("write file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		a.t.Fatalf("close multipart: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+path, &buf)
	if err != nil {
		a.t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return a.request(req)
}

// --- seeding helpers -------------------------------------------------------
//
// These go through the API too, so a seeding failure is itself a signal; they
// fail the test rather than returning errors, keeping the Convey blocks about
// the behavior under test.

func (a *api) mustCreateType(internalName, displayName string) string {
	a.t.Helper()
	resp := a.post("/api/v1/type-definitions", map[string]any{
		"internal_name": internalName, "display_name": displayName,
	})
	if resp.Status != http.StatusCreated {
		a.t.Fatalf("seed type %s: %d %s", internalName, resp.Status, resp.Body)
	}
	return resp.str(a.t, "id")
}

// mustCreateAttr creates an attribute; extra merges additional request fields
// (constraints, computed, required, ...).
func (a *api) mustCreateAttr(typeID, internalName, dataType string, extra map[string]any) string {
	a.t.Helper()
	body := map[string]any{
		"type_definition_id": typeID,
		"internal_name":      internalName,
		"display_name":       internalName,
		"data_type":          dataType,
	}
	for k, v := range extra {
		body[k] = v
	}
	resp := a.post("/api/v1/attributes", body)
	if resp.Status != http.StatusCreated {
		a.t.Fatalf("seed attribute %s: %d %s", internalName, resp.Status, resp.Body)
	}
	return resp.str(a.t, "id")
}

func (a *api) mustSetValue(typeID, attrID, entityID string, value any) map[string]any {
	a.t.Helper()
	resp := a.post("/api/v1/values", map[string]any{
		"type_definition_id":      typeID,
		"attribute_definition_id": attrID,
		"entity_id":               entityID,
		"value":                   value,
	})
	if resp.Status != http.StatusOK {
		a.t.Fatalf("seed value: %d %s", resp.Status, resp.Body)
	}
	return resp.object(a.t)
}

// missingULID is a well-formed ULID that no aggregate will ever have, for
// exercising the 404 path without tripping id-parse validation first.
const missingULID = "01J0000000000000000000000Z"
