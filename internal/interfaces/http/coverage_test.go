package http

import (
	"context"
	"net/http"
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
