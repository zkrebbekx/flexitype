package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
)

// routingServer builds a router with the console either mounted or omitted.
func routingServer(t *testing.T, disableConsole bool) *httptest.Server {
	t.Helper()
	store := memory.NewStore()
	factory := application.NewFactory(application.FactoryConfig{
		Transactor:      store.Transactor(),
		NewRepositories: func() application.Repositories { return store.Repositories() },
		ActivityLog:     store.ActivityLog(),
	})
	srv := httptest.NewServer(buildRouter(ServerConfig{
		Factory:        factory,
		Logger:         logger.New(logger.Config{Level: "error"}),
		Health:         health.NewService("flexitype", "test"),
		DisableConsole: disableConsole,
	}))
	t.Cleanup(srv.Close)
	return srv
}

// get performs a GET and returns status, content type and body.
func get(t *testing.T, srv *httptest.Server, path string) (int, string, string) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	So(err, ShouldBeNil)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	So(err, ShouldBeNil)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
}

// TestUnknownAPIPathIsJSONNotFound covers the API contract that the SPA
// fallback broke.
//
// Mounting the console as the global NotFound handler meant an unknown
// /api/v1 path answered 200 with text/html. "Endpoint absent" and "endpoint
// succeeded" were therefore the same response: a newer client against an older
// server could not negotiate, a client that checks the status before parsing
// reported an HTML parse error rather than the real cause, and a misconfigured
// caller never appeared on an error-rate dashboard.
func TestUnknownAPIPathIsJSONNotFound(t *testing.T) {
	Convey("Given an API served alongside the admin console", t, func() {
		srv := routingServer(t, false)

		Convey("When an unknown path under /api is requested", func() {
			status, contentType, body := get(t, srv, "/api/v1/does-not-exist")

			Convey("Then it is a JSON 404, not the app shell", func() {
				So(status, ShouldEqual, http.StatusNotFound)
				So(contentType, ShouldContainSubstring, "application/json")
				So(body, ShouldNotContainSubstring, "<html")

				var env struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				So(json.Unmarshal([]byte(body), &env), ShouldBeNil)
				So(env.Error.Code, ShouldEqual, "NOT_FOUND")
				So(env.Error.Message, ShouldContainSubstring, "/api/v1/does-not-exist")
			})
		})

		Convey("When a known route is requested", func() {
			status, contentType, _ := get(t, srv, "/api/v1/type-definitions")

			Convey("Then it still answers normally", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(contentType, ShouldContainSubstring, "application/json")
			})
		})

		Convey("When a method the route does not support is used", func() {
			resp, err := srv.Client().Post(srv.URL+"/api/v1/does-not-exist", "application/json",
				strings.NewReader("{}"))
			So(err, ShouldBeNil)
			defer func() { _ = resp.Body.Close() }()

			Convey("Then it is also JSON, not the app shell", func() {
				So(resp.StatusCode, ShouldEqual, http.StatusNotFound)
				So(resp.Header.Get("Content-Type"), ShouldContainSubstring, "application/json")
			})
		})

		Convey("When a non-API path is requested", func() {
			status, contentType, _ := get(t, srv, "/entities")

			Convey("Then the console still serves its client-side route", func() {
				So(status, ShouldEqual, http.StatusOK)
				So(contentType, ShouldContainSubstring, "text/html")
			})
		})
	})
}

// TestAPIOnlyModeOmitsConsole covers the deployment that must not expose a
// browser-reachable admin UI on the API listener.
func TestAPIOnlyModeOmitsConsole(t *testing.T) {
	Convey("Given an API-only deployment", t, func() {
		srv := routingServer(t, true)

		Convey("When the console root is requested", func() {
			status, contentType, _ := get(t, srv, "/")

			Convey("Then there is no app shell to serve", func() {
				So(status, ShouldEqual, http.StatusNotFound)
				So(contentType, ShouldContainSubstring, "application/json")
			})
		})

		Convey("When an unknown API path is requested", func() {
			status, _, body := get(t, srv, "/api/v1/does-not-exist")

			Convey("Then it is the same JSON 404 as with the console mounted", func() {
				So(status, ShouldEqual, http.StatusNotFound)
				So(body, ShouldContainSubstring, "NOT_FOUND")
			})
		})

		Convey("When the API is used", func() {
			status, _, _ := get(t, srv, "/api/v1/type-definitions")

			Convey("Then it is unaffected", func() {
				So(status, ShouldEqual, http.StatusOK)
			})
		})

		Convey("When the operational endpoints are used", func() {
			status, _, _ := get(t, srv, "/healthz")

			Convey("Then they are unaffected", func() {
				So(status, ShouldEqual, http.StatusOK)
			})
		})
	})
}
