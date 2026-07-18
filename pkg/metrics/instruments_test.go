package metrics

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// scrape renders the registry through the real /metrics handler, so the
// assertions below read the same numbers Prometheus would.
func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scrape returned %d", w.Code)
	}
	return w.Body.String()
}

// seriesValue pulls one series' value out of the exposition text. It returns
// -1 when the series is absent, which is distinguishable from a real 0.
func seriesValue(body, series string) float64 {
	for _, line := range strings.Split(body, "\n") {
		name, value, ok := strings.Cut(line, " ")
		if !ok || name != series {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return -1
		}
		return f
	}
	return -1
}

func TestTenantCounters(t *testing.T) {
	Convey("Given a fresh metrics instance", t, func() {
		m := New()

		Convey("When authenticated requests are counted per tenant", func() {
			m.CountTenantRequest("acme")
			m.CountTenantRequest("acme")
			m.CountTenantRequest("globex")
			body := scrape(t, m)

			Convey("Then each tenant's counter holds its own total", func() {
				So(seriesValue(body, `flexitype_tenant_requests_total{tenant="acme"}`), ShouldEqual, 2)
				So(seriesValue(body, `flexitype_tenant_requests_total{tenant="globex"}`), ShouldEqual, 1)
			})
		})

		Convey("When rate-limit rejections are counted per tenant", func() {
			m.CountRateLimitReject("acme")
			m.CountRateLimitReject("acme")
			m.CountRateLimitReject("acme")
			body := scrape(t, m)

			Convey("Then the rejection counter increments independently of requests", func() {
				So(seriesValue(body, `flexitype_ratelimit_rejected_total{tenant="acme"}`), ShouldEqual, 3)
				// No tenant request was counted, so that series was never created.
				So(seriesValue(body, `flexitype_tenant_requests_total{tenant="acme"}`), ShouldEqual, -1)
			})
		})

		Convey("When Registry is called", func() {
			reg := m.Registry()

			Convey("Then it hands back the same private registry every time", func() {
				So(reg, ShouldNotBeNil)
				So(reg, ShouldEqual, m.Registry())
			})

			Convey("Then extra collectors registered on it show up on scrape", func() {
				reg.MustRegister(&deliveryCollector{stats: fakeStats{depth: DeliveryDepth{OutboxPending: 9}}})
				So(seriesValue(scrape(t, m), "flexitype_outbox_pending"), ShouldEqual, 9)
			})
		})
	})

	Convey("Given a nil *Metrics (metrics disabled)", t, func() {
		var m *Metrics

		Convey("When the tenant counters are called", func() {
			Convey("Then they are no-ops rather than nil-pointer panics", func() {
				So(func() { m.CountTenantRequest("acme") }, ShouldNotPanic)
				So(func() { m.CountRateLimitReject("acme") }, ShouldNotPanic)
			})
		})
	})
}

func TestStatusClass(t *testing.T) {
	Convey("Given HTTP status codes across the response classes", t, func() {
		Convey("When they are bucketed for the status label", func() {
			Convey("Then each class collapses to its low-cardinality label", func() {
				So(statusClass(200), ShouldEqual, "2xx")
				So(statusClass(204), ShouldEqual, "2xx")
				So(statusClass(299), ShouldEqual, "2xx")
				So(statusClass(301), ShouldEqual, "3xx")
				So(statusClass(304), ShouldEqual, "3xx")
				So(statusClass(400), ShouldEqual, "4xx")
				So(statusClass(429), ShouldEqual, "4xx")
				So(statusClass(500), ShouldEqual, "5xx")
				So(statusClass(503), ShouldEqual, "5xx")
			})

			Convey("Then sub-200 codes fall through to the literal code", func() {
				So(statusClass(100), ShouldEqual, "100")
				So(statusClass(0), ShouldEqual, "0")
			})
		})
	})
}

// unflushableWriter deliberately does not implement http.Flusher, so the
// statusWriter's Flush must degrade to a no-op rather than panicking.
type unflushableWriter struct{ http.ResponseWriter }

func TestStatusWriter(t *testing.T) {
	Convey("Given the metrics middleware's status-recording writer", t, func() {
		Convey("When the wrapped writer supports flushing", func() {
			rec := httptest.NewRecorder()
			sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
			sw.WriteHeader(http.StatusAccepted)
			_, _ = sw.Write([]byte("event: ping\n\n"))
			sw.Flush()

			Convey("Then the status is captured and the flush reaches the writer", func() {
				So(sw.status, ShouldEqual, http.StatusAccepted)
				So(rec.Code, ShouldEqual, http.StatusAccepted)
				So(rec.Flushed, ShouldBeTrue)
				So(rec.Body.String(), ShouldEqual, "event: ping\n\n")
			})
		})

		Convey("When the wrapped writer does not support flushing", func() {
			rec := httptest.NewRecorder()
			sw := &statusWriter{ResponseWriter: unflushableWriter{rec}, status: http.StatusOK}

			Convey("Then Flush is a safe no-op", func() {
				So(func() { sw.Flush() }, ShouldNotPanic)
				So(rec.Flushed, ShouldBeFalse)
			})
		})
	})

	Convey("Given a streaming handler behind the metrics middleware", t, func() {
		m := New()
		handler := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("chunk"))
			w.(http.Flusher).Flush()
		}))

		Convey("When the response is streamed outside any chi route", func() {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))
			body := scrape(t, m)

			Convey("Then SSE still streams through and the request counts as unmatched", func() {
				So(rec.Flushed, ShouldBeTrue)
				So(rec.Body.String(), ShouldEqual, "chunk")
				So(seriesValue(body, `flexitype_http_requests_total{method="GET",route="unmatched",status="2xx"}`),
					ShouldEqual, 1)
			})

			Convey("Then the in-flight gauge is back to zero once the handler returns", func() {
				So(seriesValue(body, "flexitype_http_requests_in_flight"), ShouldEqual, 0)
			})
		})
	})
}
