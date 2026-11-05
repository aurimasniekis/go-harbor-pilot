package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds Prometheus collectors. Labels are deliberately
// low-cardinality: protocol, provider kind, outcome class and similar bounded
// enumerations. Request, session, account, token, model and project
// identifiers are never labels.
type Metrics struct {
	Registry *prometheus.Registry

	HTTPRequests *prometheus.CounterVec

	GatewayRequests   *prometheus.CounterVec
	GatewayAttempts   *prometheus.CounterVec
	GatewayTTFT       *prometheus.HistogramVec
	GatewayDuration   *prometheus.HistogramVec
	GatewayInflight   prometheus.Gauge
	TelemetryDropped  *prometheus.CounterVec
	OutboxPending     *prometheus.GaugeVec
	DeliveriesTotal   *prometheus.CounterVec
	BreakerState      *prometheus.GaugeVec
	ReservationsTotal *prometheus.CounterVec
	ConfigRevision    prometheus.Gauge
}

// NewMetrics registers collectors on a fresh registry.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	latency := []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300}
	m := &Metrics{
		Registry: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hp_http_requests_total", Help: "HTTP requests by route pattern and status class.",
		}, []string{"route", "class"}),
		GatewayRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hp_gateway_requests_total", Help: "Logical gateway requests by inbound protocol and outcome.",
		}, []string{"protocol", "outcome"}),
		GatewayAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hp_gateway_attempts_total", Help: "Upstream attempts by executor kind and outcome class.",
		}, []string{"executor", "outcome"}),
		GatewayTTFT: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "hp_gateway_ttft_seconds", Help: "Time to first token by executor kind.", Buckets: latency,
		}, []string{"executor"}),
		GatewayDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "hp_gateway_request_duration_seconds", Help: "Total logical request duration by inbound protocol.", Buckets: latency,
		}, []string{"protocol"}),
		GatewayInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "hp_gateway_inflight_requests", Help: "Logical requests currently in flight.",
		}),
		TelemetryDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hp_telemetry_dropped_total", Help: "Telemetry items dropped under back-pressure by pipeline.",
		}, []string{"pipeline"}),
		OutboxPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hp_outbox_pending", Help: "Undelivered outbox rows by consumer.",
		}, []string{"consumer"}),
		DeliveriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hp_deliveries_total", Help: "Event and notification delivery attempts by channel kind and outcome.",
		}, []string{"channel", "outcome"}),
		BreakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hp_compat_breakers", Help: "Compatibility circuit breakers by state.",
		}, []string{"state"}),
		ReservationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hp_reservations_total", Help: "Budget/concurrency reservations by outcome.",
		}, []string{"outcome"}),
		ConfigRevision: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "hp_config_revision", Help: "Active compiled configuration revision.",
		}),
	}
	reg.MustRegister(m.HTTPRequests, m.GatewayRequests, m.GatewayAttempts, m.GatewayTTFT, m.GatewayDuration,
		m.GatewayInflight, m.TelemetryDropped, m.OutboxPending, m.DeliveriesTotal, m.BreakerState,
		m.ReservationsTotal, m.ConfigRevision)
	return m
}
