package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/smartystreets/goconvey/convey"
)

type fakeStats struct {
	depth DeliveryDepth
	err   error
}

func (f fakeStats) Snapshot(context.Context) (DeliveryDepth, error) { return f.depth, f.err }

func TestMetrics(t *testing.T) {
	Convey("Given a metrics instance wired into a chi router", t, func() {
		m := New()
		r := chi.NewRouter()
		r.Use(m.Middleware)
		r.Get("/api/v1/things/{id}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		r.Get("/boom", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		r.Handle("/metrics", m.Handler())

		serve := func(method, path string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			return w
		}

		Convey("When requests are served and /metrics scraped", func() {
			serve("GET", "/api/v1/things/abc")
			serve("GET", "/api/v1/things/xyz")
			serve("GET", "/boom")

			body := serve("GET", "/metrics").Body.String()

			Convey("Then per-route counters use the route pattern, not the raw id", func() {
				So(body, ShouldContainSubstring,
					`flexitype_http_requests_total{method="GET",route="/api/v1/things/{id}",status="2xx"} 2`)
				So(body, ShouldContainSubstring, `route="/boom",status="5xx"`)
				// The raw ids must not appear as labels (cardinality guard).
				So(body, ShouldNotContainSubstring, "things/abc")
				So(body, ShouldContainSubstring, "flexitype_http_request_duration_seconds")
			})
		})

		Convey("When the delivery collector is registered", func() {
			m.RegisterDeliveryCollector(fakeStats{depth: DeliveryDepth{
				OutboxPending:      3,
				DeliveriesByStatus: map[string]int64{"pending": 2, "dead": 1},
			}})
			body := serve("GET", "/metrics").Body.String()

			Convey("Then depth gauges are exposed on scrape", func() {
				So(body, ShouldContainSubstring, "flexitype_outbox_pending 3")
				So(body, ShouldContainSubstring, `flexitype_webhook_deliveries{status="pending"} 2`)
				So(body, ShouldContainSubstring, `flexitype_webhook_deliveries{status="dead"} 1`)
				So(body, ShouldContainSubstring, `flexitype_webhook_deliveries{status="delivered"} 0`)
			})
		})

		Convey("When the stats query fails at scrape time", func() {
			m.RegisterDeliveryCollector(fakeStats{err: context.DeadlineExceeded})

			Convey("Then the scrape still succeeds, omitting the depth gauges", func() {
				w := serve("GET", "/metrics")
				So(w.Code, ShouldEqual, http.StatusOK)
				So(w.Body.String(), ShouldNotContainSubstring, "flexitype_outbox_pending")
			})
		})
	})
}
