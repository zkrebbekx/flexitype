package http

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/api"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/gql"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
)

// TestOpenAPIRouteCoverage keeps the committed OpenAPI document exhaustive: it
// walks the actual chi router with every optional feature enabled and fails if
// any non-operational REST route lacks an operation in the spec, so a
// generated client can never silently omit a shipped endpoint.
func TestOpenAPIRouteCoverage(t *testing.T) {
	Convey("Given the live router and the committed OpenAPI document", t, func() {
		router := buildRouter(ServerConfig{
			Logger:  logger.New(logger.Config{}),
			Health:  health.NewService("flexitype", "test"),
			Metrics: metrics.New(),
			Admin:   &admin.Interactor{}, // non-nil registers the provisioning routes
			GraphQL: gql.NewEngine(),
			Reindex: func(context.Context, valueobjects.TenantID) (int, error) { return 0, nil },
		})

		documented := documentedOperations(t)

		var missing []string
		err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if !strings.HasPrefix(route, "/api/v1/") || strings.HasPrefix(route, "/api/v1/openapi") {
				return nil // operational + spec endpoints are exempt
			}
			key := method + " " + strings.TrimSuffix(route, "/")
			if !documented[key] {
				missing = append(missing, key)
			}
			return nil
		})
		So(err, ShouldBeNil)

		Convey("Then every REST route is documented", func() {
			sort.Strings(missing)
			So(strings.Join(missing, "\n"), ShouldEqual, "")
		})
	})
}

func documentedOperations(t *testing.T) map[string]bool {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.SpecYAML)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	out := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			out[method+" /api/v1"+path] = true
		}
	}
	return out
}

// TestClientRouteCoverage keeps the Go SDK in step with the router: it walks
// the same routes and fails when one has no method in the client module.
//
// Eight capabilities had shipped with no client method, including both
// right-to-erasure endpoints. Each gap pushed part of an integration back to
// raw net/http, so a codebase that standardised on the SDK ended up with two
// client styles and two error-handling conventions for one service.
//
// This is a smoke test, not a proof. The client builds paths by
// concatenation, so it cannot match a route template exactly; it asserts that
// the route's last literal segment appears in a path string somewhere in the
// client. It catches "this route has no method at all", which is the defect
// that occurred. Two things it does not catch:
//
//   - A method that targets the wrong path. The conformance suite covers
//     that, by calling the SDK against a running server.
//   - A missing method for one of two routes that end in the same segment.
//     POST /admin/purge and POST /entities/{t}/{e}/purge both end in
//     "/purge", so either method alone satisfies both.
//
// The client is a separate module, so this reads its source rather than
// importing it.
func TestClientRouteCoverage(t *testing.T) {
	// notInSDK lists routes deliberately absent, each with its reason. An
	// entry here is a decision, not an oversight.
	notInSDK := map[string]string{
		"GET /api/v1/events/stream": "server-sent events; the SDK exposes " +
			"Events().ListPage cursor paging instead of a streaming transport",
		"GET /api/v1/graphql":  "GraphQL is a whole second query language; use any GraphQL client",
		"POST /api/v1/graphql": "GraphQL is a whole second query language; use any GraphQL client",
	}

	Convey("Given the live router and the Go client's source", t, func() {
		router := buildRouter(ServerConfig{
			Logger:  logger.New(logger.Config{}),
			Health:  health.NewService("flexitype", "test"),
			Metrics: metrics.New(),
			Admin:   &admin.Interactor{},
			GraphQL: gql.NewEngine(),
			Reindex: func(context.Context, valueobjects.TenantID) (int, error) { return 0, nil },
		})
		source := clientSource(t)

		var missing []string
		err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			route = strings.TrimSuffix(route, "/")
			if !strings.HasPrefix(route, "/api/v1/") {
				return nil
			}
			key := method + " " + route
			if _, ok := notInSDK[key]; ok || strings.HasPrefix(route, "/api/v1/openapi") {
				return nil // the spec endpoints serve the document itself
			}
			seg := lastLiteralSegment(route)
			if seg == "" || clientMentions(source, seg) {
				return nil
			}
			missing = append(missing, key+"  (looked for "+seg+")")
			return nil
		})
		So(err, ShouldBeNil)

		Convey("Then every route has a client method", func() {
			sort.Strings(missing)
			So(strings.Join(missing, "\n"), ShouldEqual, "")
		})
	})
}

// lastLiteralSegment returns the deepest non-parameter segment of a route,
// with its leading slash — the string the client must build to reach it.
// "/api/v1/entities/{t}/{e}/purge" gives "/purge"; "/api/v1/values/{id}"
// gives "/values".
func lastLiteralSegment(route string) string {
	segs := strings.Split(strings.TrimPrefix(route, "/api/v1"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		s := segs[i]
		if s == "" || strings.HasPrefix(s, "{") {
			continue
		}
		return "/" + s
	}
	return ""
}

// clientMentions reports whether the client source builds a path reaching
// seg. It accepts the segment with its slash ("/purge") and bare in a quoted
// literal ("submit"), because transitions are built as
// "/changesets/"+id+"/"+action.
func clientMentions(source, seg string) bool {
	bare := strings.TrimPrefix(seg, "/")
	return strings.Contains(source, `"`+seg) ||
		strings.Contains(source, seg+`"`) ||
		strings.Contains(source, `"`+bare+`"`)
}

// clientSource concatenates the client module's Go source. The client is a
// separate module, so this cannot be an import.
func clientSource(t *testing.T) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "client", "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob client sources: %v (found %d)", err, len(paths))
	}
	var b strings.Builder
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		b.Write(body)
	}
	return b.String()
}
