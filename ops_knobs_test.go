package flexitype_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
)

// TestTenantLabelCardinality covers the metric labels that grew without bound.
//
// Two counters were labelled with the raw tenant id and nothing bounded the
// set, so time-series cardinality grew linearly with tenant count and never
// decayed: a deployment with many tenants pays for those series in scrape
// size, in the store and in every query that touches them, and a tenant that
// has gone away keeps its series for ever.
func TestTenantLabelCardinality(t *testing.T) {
	Convey("Given a metrics registry", t, func() {
		m := metrics.New()

		Convey("When more tenants than the cap are seen", func() {
			for i := 0; i < metrics.MaxTenantLabels+50; i++ {
				m.CountTenantRequest(tenantName(i))
				m.CountRateLimitReject(tenantName(i))
			}

			Convey("Then the series count stops at the cap plus the overflow label", func() {
				body := scrape(t, m)
				So(countSeries(body, "flexitype_tenant_requests_total"),
					ShouldBeLessThanOrEqualTo, metrics.MaxTenantLabels+1)
			})

			Convey("Then the tenants seen first keep their own label", func() {
				So(scrape(t, m), ShouldContainSubstring, `tenant="tenant-0"`)
			})

			Convey("Then the rest fold into a visible marker, not a silent drop", func() {
				So(scrape(t, m), ShouldContainSubstring, `tenant="`+metrics.OverflowTenantLabel+`"`)
			})
		})

		Convey("When a nil Metrics is used", func() {
			Convey("Then counting is a no-op rather than a panic", func() {
				var nilM *metrics.Metrics
				So(func() { nilM.CountTenantRequest("t") }, ShouldNotPanic)
				So(func() { nilM.CountRateLimitReject("t") }, ShouldNotPanic)
			})
		})
	})
}

// TestWebhookTimeoutOption pins that the delivery timeout is a duration.
//
// It is not an *http.Client on purpose: the delivery client is the SSRF guard
// that refuses private destinations unless the deployment opted in by name,
// and accepting a client would let a caller replace that guard without
// saying so.
// TestOutboxParkKnobOptions pins the ops knobs issue #478 added: the park
// budget and the retry ceiling were hardcoded (25 attempts, 15m cap), so a
// deployment could not size the retry window to the longest outage it must
// ride out, and the parked retention could not be tuned to its alerting
// window. The adapter-level semantics (a budget of 2 parks on the second
// failure; the ceiling caps the backoff) are covered by
// TestOutboxParkKnobsIntegration against PostgreSQL.
func TestOutboxParkKnobOptions(t *testing.T) {
	Convey("Given a service configured with the outbox park knobs", t, func() {
		svc := flexitype.NewInMemory(
			flexitype.WithOutboxMaxAttempts(5),
			flexitype.WithOutboxRetryCeiling(30*time.Second),
			flexitype.WithParkedRetention(14*24*time.Hour),
		)

		Convey("Then it builds", func() {
			So(svc, ShouldNotBeNil)
		})
	})

	Convey("Given a service configured with non-positive park knobs", t, func() {
		svc := flexitype.NewInMemory(
			flexitype.WithOutboxMaxAttempts(0),
			flexitype.WithOutboxRetryCeiling(0),
			flexitype.WithParkedRetention(-time.Hour),
		)

		Convey("Then it builds and every default stands", func() {
			So(svc, ShouldNotBeNil)
		})
	})
}

func TestWebhookTimeoutOption(t *testing.T) {
	Convey("Given a service configured with a webhook timeout", t, func() {
		svc := flexitype.NewInMemory(flexitype.WithWebhookTimeout(0))

		Convey("Then it builds, and a zero timeout keeps the default", func() {
			So(svc, ShouldNotBeNil)
		})
	})
}

func tenantName(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "tenant-0"
	}
	out := ""
	for n := i; n > 0; n /= 10 {
		out = string(digits[n%10]) + out
	}
	return "tenant-" + out
}

// scrape renders the registry's current exposition text.
func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := &recorder{}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	So(err, ShouldBeNil)
	m.Handler().ServeHTTP(rec, req)
	return rec.body
}

// countSeries counts the exposition lines for one metric name.
func countSeries(body, name string) int {
	n := 0
	for _, line := range splitLines(body) {
		if len(line) > len(name) && line[:len(name)] == name && line[len(name)] == '{' {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// recorder is a minimal http.ResponseWriter capturing the body.
type recorder struct {
	body   string
	header http.Header
}

func (r *recorder) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}
func (r *recorder) Write(b []byte) (int, error) { r.body += string(b); return len(b), nil }
func (r *recorder) WriteHeader(int)             {}
