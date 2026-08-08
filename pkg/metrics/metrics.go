// Package metrics exposes Prometheus SLIs for the standalone service: HTTP
// request rates and latencies, plus event-delivery depth gauges collected
// on scrape. It is opt-in — the service registers and serves /metrics only
// when metrics are enabled.
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/zkrebbekx/flexitype/pkg/deliverystats"
)

// Metrics holds the registry and the instruments the service reports.
type Metrics struct {
	registry        *prometheus.Registry
	httpRequests    *prometheus.CounterVec
	httpDuration    *prometheus.HistogramVec
	httpInFlight    prometheus.Gauge
	tenantRequests  *prometheus.CounterVec
	rateLimitReject *prometheus.CounterVec

	// mu guards tenantLabels, the bounded set of tenant ids admitted as a
	// metric label.
	mu           sync.RWMutex
	tenantLabels map[string]bool
}

// New builds a Metrics with a private registry (Go runtime + process
// collectors included) and the HTTP instruments registered.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: reg,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "flexitype_http_requests_total",
			Help: "Total HTTP requests by method, route and status class.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "flexitype_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "flexitype_http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		tenantRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "flexitype_tenant_requests_total",
			Help: "Authenticated API requests by tenant.",
		}, []string{"tenant"}),
		rateLimitReject: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "flexitype_ratelimit_rejected_total",
			Help: "Requests rejected by the rate limiter, by tenant.",
		}, []string{"tenant"}),
		tenantLabels: map[string]bool{},
	}
	reg.MustRegister(m.httpRequests, m.httpDuration, m.httpInFlight, m.tenantRequests, m.rateLimitReject)
	return m
}

// MaxTenantLabels bounds how many distinct tenant ids appear as a metric
// label. Everything beyond it is folded into OverflowTenantLabel.
//
// The two tenant counters were labelled with the raw tenant id and nothing
// bounded the set, so time-series cardinality grew linearly with tenant count
// and never decayed: a deployment with many tenants pays for those series in
// scrape size, in the store, and in every query that touches them — and the
// series for a tenant that has gone away never expire.
//
// The bound is deliberately generous. A deployment with fewer tenants than
// this keeps exact per-tenant visibility; one with more keeps it for the
// tenants it saw first and can still see the aggregate.
const MaxTenantLabels = 100

// OverflowTenantLabel is the label value that stands for every tenant beyond
// MaxTenantLabels. It is a visible, greppable marker rather than a silent
// drop: an operator has to be able to tell "no traffic" from "not labelled".
const OverflowTenantLabel = "other"

// tenantLabel maps a tenant id to the label to record under, admitting new
// ids until the cap is reached.
func (m *Metrics) tenantLabel(tenant string) string {
	m.mu.RLock()
	known := m.tenantLabels[tenant]
	full := len(m.tenantLabels) >= MaxTenantLabels
	m.mu.RUnlock()
	if known {
		return tenant
	}
	if full {
		return OverflowTenantLabel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Re-check under the write lock: another request may have taken the last
	// slot in between.
	if m.tenantLabels[tenant] {
		return tenant
	}
	if len(m.tenantLabels) >= MaxTenantLabels {
		return OverflowTenantLabel
	}
	m.tenantLabels[tenant] = true
	return tenant
}

// CountTenantRequest records one authenticated request for a tenant. Safe
// on a nil Metrics.
func (m *Metrics) CountTenantRequest(tenant string) {
	if m == nil {
		return
	}
	m.tenantRequests.WithLabelValues(m.tenantLabel(tenant)).Inc()
}

// CountRateLimitReject records one rate-limited request for a tenant. Safe
// on a nil Metrics.
func (m *Metrics) CountRateLimitReject(tenant string) {
	if m == nil {
		return
	}
	m.rateLimitReject.WithLabelValues(m.tenantLabel(tenant)).Inc()
}

// Registry exposes the underlying registry so callers can register extra
// collectors (e.g. the delivery-depth collector).
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler serves the metrics in the Prometheus text exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Middleware records request counts, in-flight gauge and latency. The
// route label is the chi route pattern (bounded cardinality), not the raw
// path, so per-id URLs don't explode the series count.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.httpInFlight.Inc()
		defer m.httpInFlight.Dec()

		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		m.httpRequests.WithLabelValues(r.Method, route, statusClass(rec.status)).Inc()
		m.httpDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// statusClass buckets status codes into 2xx/4xx/5xx labels to keep
// cardinality low while preserving the signal alerts care about.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return strconv.Itoa(code)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so SSE responses still stream
// through the metrics middleware.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// DeliveryStats reports current event-delivery depth. The postgres layer
// implements it; the collector below turns it into scrape-time gauges.
//
// The contract itself lives in pkg/deliverystats, which depends on the
// standard library alone, so the storage layer can satisfy it without
// importing this package — and with it chi and the Prometheus client. These
// aliases keep the old names working.
type DeliveryStats = deliverystats.Source

// DeliveryDepth is a point-in-time view of the outbox and delivery queues.
type DeliveryDepth = deliverystats.Depth

// RegisterDeliveryCollector wires a scrape-time collector over stats. The
// registry queries stats.Snapshot when Prometheus scrapes, so the gauges
// are always fresh without a background poller.
func (m *Metrics) RegisterDeliveryCollector(stats DeliveryStats) {
	m.registry.MustRegister(&deliveryCollector{stats: stats})
}

type deliveryCollector struct {
	stats DeliveryStats
}

var (
	outboxPendingDesc = prometheus.NewDesc(
		"flexitype_outbox_pending",
		"Undispatched envelopes the relay will still retry. Excludes parked envelopes.", nil, nil)
	// Parked envelopes are committed changes no external consumer has seen,
	// waiting for an operator redrive. Alert on non-zero: nothing but
	// POST /admin/outbox/redrive moves them, and the parked retention
	// eventually deletes them.
	outboxParkedDesc = prometheus.NewDesc(
		"flexitype_outbox_parked",
		"Envelopes parked after exhausting their retry budget, awaiting an operator redrive.", nil, nil)
	deliveriesDesc = prometheus.NewDesc(
		"flexitype_webhook_deliveries", "Webhook deliveries by status.", []string{"status"}, nil)
	// The expansion lag: how long the oldest undispatched envelope has been
	// waiting. A depth alone cannot tell a healthy backlog from a stalled
	// relay — 500 pending is normal under load and alarming if the oldest of
	// them is an hour old. Alert on this, not on the count.
	oldestPendingDesc = prometheus.NewDesc(
		"flexitype_outbox_oldest_pending_seconds",
		"Age of the oldest undispatched envelope, in seconds. 0 when none is pending.", nil, nil)
)

func (c *deliveryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- outboxPendingDesc
	ch <- outboxParkedDesc
	ch <- deliveriesDesc
	ch <- oldestPendingDesc
}

func (c *deliveryCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	depth, err := c.stats.Snapshot(ctx)
	if err != nil {
		// A scrape-time query failure should not break the whole scrape;
		// simply omit the gauges this round.
		return
	}
	ch <- prometheus.MustNewConstMetric(outboxPendingDesc, prometheus.GaugeValue, float64(depth.OutboxPending))
	ch <- prometheus.MustNewConstMetric(outboxParkedDesc, prometheus.GaugeValue, float64(depth.OutboxParked))
	ch <- prometheus.MustNewConstMetric(oldestPendingDesc, prometheus.GaugeValue, depth.OldestPendingAge.Seconds())
	for _, status := range []string{"pending", "inflight", "delivered", "dead"} {
		ch <- prometheus.MustNewConstMetric(deliveriesDesc, prometheus.GaugeValue,
			float64(depth.DeliveriesByStatus[status]), status)
	}
}
