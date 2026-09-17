package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds",
		Help: "HTTP request duration in seconds",
	}, []string{"method", "path", "status"})

	CircuitBreakerState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "circuit_breaker_state",
		Help: "Circuit breaker state: 0=closed, 1=open, 2=half_open",
	}, []string{"name"})

	OutboxEventsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "outbox_events_processed_total",
		Help: "Outbox events processed by the worker pool",
	}, []string{"event_type", "result"})
)

func init() {
	prometheus.MustRegister(HTTPRequestDuration, CircuitBreakerState, OutboxEventsProcessed)
}
