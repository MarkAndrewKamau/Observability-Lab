// Package metrics defines the Prometheus instruments shared across services:
// the RED signals (Rate, Errors, Duration) for HTTP and for queue work. These
// become the raw material for the SLO recording rules and burn-rate alerts.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics bundles the instruments for one service.
type Metrics struct {
	reg *prometheus.Registry

	HTTPRequests *prometheus.CounterVec   // by method, route, status
	HTTPDuration *prometheus.HistogramVec // by method, route

	// Transaction-level signals — these drive the SLOs (success rate + latency)
	// rather than pure infra metrics.
	Transactions        *prometheus.CounterVec   // by type, outcome (success|failure)
	TransactionDuration *prometheus.HistogramVec // by type

	QueuePublished *prometheus.CounterVec // by queue
	QueueConsumed  *prometheus.CounterVec // by queue, outcome
}

// New creates and registers the instruments for a service.
func New(service string) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector())
	labels := prometheus.Labels{"service": service}

	m := &Metrics{
		reg: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "HTTP requests.", ConstLabels: labels,
		}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds", Help: "HTTP request latency.",
			Buckets: prometheus.DefBuckets, ConstLabels: labels,
		}, []string{"method", "route"}),
		Transactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "transactions_total", Help: "Business transactions by outcome.", ConstLabels: labels,
		}, []string{"type", "outcome"}),
		TransactionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "transaction_duration_seconds", Help: "End-to-end transaction latency.",
			// Buckets tuned to a ~1s objective for the SLO.
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5}, ConstLabels: labels,
		}, []string{"type"}),
		QueuePublished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "queue_published_total", Help: "Messages published.", ConstLabels: labels,
		}, []string{"queue"}),
		QueueConsumed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "queue_consumed_total", Help: "Messages consumed by outcome.", ConstLabels: labels,
		}, []string{"queue", "outcome"}),
	}
	reg.MustRegister(m.HTTPRequests, m.HTTPDuration, m.Transactions,
		m.TransactionDuration, m.QueuePublished, m.QueueConsumed)
	return m
}

// Handler returns the /metrics HTTP handler for this service's registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
