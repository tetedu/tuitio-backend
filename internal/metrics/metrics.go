// Package metrics exposes Prometheus instrumentation for the Tuitio backend.
//
// A dedicated registry is used (rather than the process-wide default) so the
// collectors can be exercised by unit tests without colliding with the global
// default registry or triggering duplicate-registration panics.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds Tuitio's collectors. It is intentionally not the global
// default registry so tests can scrape it in isolation.
var Registry = prometheus.NewRegistry()

var (
	indexerLag = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tuitio_indexer_lag_ledgers",
		Help: "How many ledgers the indexer is behind the network tip.",
	})
	indexerLastProgress = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tuitio_indexer_last_progress_unix_seconds",
		Help: "Unix time of the indexer's last recorded progress, or 0 if never.",
	})
	httpInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tuitio_http_requests_in_flight",
		Help: "Number of API requests currently being handled.",
	})
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tuitio_http_requests_total",
		Help: "Total number of API requests handled, labelled by method and status code.",
	}, []string{"method", "code"})
	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tuitio_http_request_duration_seconds",
		Help:    "API request latency in seconds, labelled by method and status code.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"method", "code"})
)

func init() {
	Registry.MustRegister(
		indexerLag,
		indexerLastProgress,
		httpInFlight,
		httpRequestsTotal,
		httpRequestDuration,
	)
}

// SetIndexerLag records how far the indexer is behind. A negative lag (the
// cursor has caught up with or passed the tip) is clamped to zero.
func SetIndexerLag(ledgersBehind int64) {
	if ledgersBehind < 0 {
		ledgersBehind = 0
	}
	indexerLag.Set(float64(ledgersBehind))
}

// RecordIndexerProgress updates the indexer lag and last-progress timestamp
// from the cursor the indexer has reached and the network's latest ledger.
func RecordIndexerProgress(cursor, latest int64) {
	SetIndexerLag(latest - cursor)
	indexerLastProgress.Set(float64(time.Now().Unix()))
}

// Handler returns the Prometheus scrape endpoint handler bound to Registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}

// MetricsMiddleware records latency, in-flight count, and totals for every
// request handled by the wrapped handler. The /metrics scrape endpoint is
// passed through untouched so it never measures itself.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		httpInFlight.Inc()
		defer httpInFlight.Dec()

		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		elapsed := time.Since(start).Seconds()

		code := strconv.Itoa(sw.status)
		httpRequestsTotal.WithLabelValues(r.Method, code).Inc()
		httpRequestDuration.WithLabelValues(r.Method, code).Observe(elapsed)
	})
}

// statusWriter captures the response status code without altering behavior.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
