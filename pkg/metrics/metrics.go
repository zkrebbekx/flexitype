// Package metrics exposes Prometheus SLIs for the standalone service: HTTP
// request rates and latencies, plus event-delivery depth gauges collected
// on scrape. It is opt-in — the service registers and serves /metrics only
// when metrics are enabled.
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the registry and the instruments the service reports.
type Metrics struct {
	registry     *prometheus.Registry
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge
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
	}
	reg.MustRegister(m.httpRequests, m.httpDuration, m.httpInFlight)
	return m
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
type DeliveryStats interface {
	// Snapshot returns current counts. Called on each scrape, so it must be
	// cheap (indexed COUNT queries).
	Snapshot(ctx context.Context) (DeliveryDepth, error)
}

// DeliveryDepth is a point-in-time view of the outbox and delivery queues.
type DeliveryDepth struct {
	OutboxPending      int64
	DeliveriesByStatus map[string]int64 // pending / inflight / delivered / dead
}

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
		"flexitype_outbox_pending", "Undispatched envelopes awaiting expansion.", nil, nil)
	deliveriesDesc = prometheus.NewDesc(
		"flexitype_webhook_deliveries", "Webhook deliveries by status.", []string{"status"}, nil)
)

func (c *deliveryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- outboxPendingDesc
	ch <- deliveriesDesc
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
	for _, status := range []string{"pending", "inflight", "delivered", "dead"} {
		ch <- prometheus.MustNewConstMetric(deliveriesDesc, prometheus.GaugeValue,
			float64(depth.DeliveriesByStatus[status]), status)
	}
}
