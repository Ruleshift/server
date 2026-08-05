package gamejampromo

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry      *prometheus.Registry
	discoveryRuns *prometheus.CounterVec
	lastSuccess   *prometheus.GaugeVec
	pending       prometheus.Gauge
	verifications *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	value := &Metrics{
		registry: prometheus.NewRegistry(),
		discoveryRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ruleshift_gamejam_discovery_runs_total", Help: "Game jam discovery runs by bounded source and result.",
		}, []string{"source", "result"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "ruleshift_gamejam_discovery_last_success_timestamp_seconds", Help: "Unix timestamp of the last successful discovery run.",
		}, []string{"source"}),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "ruleshift_gamejam_pending_candidates", Help: "Candidates waiting for moderation.",
		}),
		verifications: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ruleshift_gamejam_code_verifications_total", Help: "Promotion code verification attempts by bounded result.",
		}, []string{"result"}),
	}
	value.registry.MustRegister(value.discoveryRuns, value.lastSuccess, value.pending, value.verifications)
	return value
}

func (m *Metrics) ObserveDiscovery(source, result string, at time.Time) {
	m.discoveryRuns.WithLabelValues(source, result).Inc()
	if result == "success" {
		m.lastSuccess.WithLabelValues(source).Set(float64(at.Unix()))
	}
}

func (m *Metrics) SetPending(count int)              { m.pending.Set(float64(count)) }
func (m *Metrics) ObserveVerification(result string) { m.verifications.WithLabelValues(result).Inc() }
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
