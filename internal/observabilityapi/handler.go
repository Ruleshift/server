package observabilityapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/operations"
)

const maxUpstreamBody = 1 << 20

type Thresholds struct {
	StaleAfter       time.Duration
	UnavailableAfter time.Duration
	ErrorRatio       float64
	CommandP95       time.Duration
	QueueSaturation  float64
	SlowConsumerRate float64
	MinCommands      float64
}

func DefaultThresholds() Thresholds {
	return Thresholds{StaleAfter: 30 * time.Second, UnavailableAfter: 60 * time.Second, ErrorRatio: .01, CommandP95: 250 * time.Millisecond, QueueSaturation: .8, SlowConsumerRate: .005, MinCommands: 100}
}

type Config struct {
	PrometheusURL  string
	OperationsURL  string
	AllowedOrigin  string
	GrafanaSystem  string
	GrafanaRuntime string
	Timeout        time.Duration
	CacheTTL       time.Duration
	Thresholds     Thresholds
}

type Handler struct {
	cfg        Config
	prometheus *PrometheusClient
	upstream   *http.Client
	mux        *http.ServeMux
	cacheMu    sync.Mutex
	cached     Overview
	cachedAt   time.Time
}

type Overview struct {
	Status          string             `json:"status"`
	Reasons         []string           `json:"reasons"`
	GeneratedAt     time.Time          `json:"generated_at"`
	SourceTimestamp time.Time          `json:"source_timestamp,omitempty"`
	Metrics         OverviewMetrics    `json:"metrics"`
	Thresholds      OverviewThresholds `json:"thresholds"`
	Grafana         GrafanaLinks       `json:"grafana"`
}

type OverviewMetrics struct {
	ActiveRooms          float64 `json:"active_rooms"`
	Connections          float64 `json:"connections"`
	CommandRate          float64 `json:"command_rate_per_second"`
	ServerErrorRatio     float64 `json:"server_error_ratio"`
	CommandP95Seconds    float64 `json:"command_p95_seconds"`
	QueueSaturationRatio float64 `json:"queue_saturation_ratio"`
	SlowConsumerRatio    float64 `json:"slow_consumer_ratio"`
}

type OverviewThresholds struct {
	StaleAfterSeconds       float64 `json:"stale_after_seconds"`
	UnavailableAfterSeconds float64 `json:"unavailable_after_seconds"`
	ErrorRatio              float64 `json:"server_error_ratio"`
	CommandP95Seconds       float64 `json:"command_p95_seconds"`
	QueueSaturationRatio    float64 `json:"queue_saturation_ratio"`
	SlowConsumerRatio       float64 `json:"slow_consumer_ratio"`
	MinCommands             float64 `json:"min_commands"`
}

type GrafanaLinks struct {
	SystemOverview     string `json:"system_overview,omitempty"`
	RuntimeDiagnostics string `json:"runtime_diagnostics,omitempty"`
}

func New(cfg Config) (*Handler, error) {
	if cfg.PrometheusURL == "" || cfg.OperationsURL == "" {
		return nil, fmt.Errorf("Prometheus and operations URLs are required")
	}
	if _, err := url.ParseRequestURI(cfg.PrometheusURL); err != nil {
		return nil, fmt.Errorf("invalid Prometheus URL: %w", err)
	}
	if _, err := url.ParseRequestURI(cfg.OperationsURL); err != nil {
		return nil, fmt.Errorf("invalid operations URL: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Second
	}
	if cfg.Thresholds.StaleAfter <= 0 {
		cfg.Thresholds = DefaultThresholds()
	}
	client := &http.Client{Timeout: cfg.Timeout}
	h := &Handler{cfg: cfg, prometheus: NewPrometheusClient(strings.TrimRight(cfg.PrometheusURL, "/"), client), upstream: client, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /healthz", h.health)
	h.mux.HandleFunc("GET /v1/overview", h.overview)
	h.mux.HandleFunc("GET /v1/rooms", h.rooms)
	h.mux.HandleFunc("GET /v1/rooms/{public_room_ref}", h.room)
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if origin := r.Header.Get("Origin"); origin != "" && origin == h.cfg.AllowedOrigin {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		if r.Header.Get("Origin") != h.cfg.AllowedOrigin {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	h.cacheMu.Lock()
	if !h.cachedAt.IsZero() && time.Since(h.cachedAt) < h.cfg.CacheTTL {
		value := h.cached
		h.cacheMu.Unlock()
		w.Header().Set("Cache-Control", "public, max-age=5, stale-while-revalidate=10")
		writeJSON(w, http.StatusOK, value)
		return
	}
	h.cacheMu.Unlock()

	value := h.buildOverview(r.Context())
	h.cacheMu.Lock()
	h.cached, h.cachedAt = value, time.Now()
	h.cacheMu.Unlock()
	w.Header().Set("Cache-Control", "public, max-age=5, stale-while-revalidate=10")
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) buildOverview(ctx context.Context) Overview {
	queries := map[string]string{
		"up":            `max(ruleshift_up)`,
		"up_timestamp":  `max(timestamp(ruleshift_up))`,
		"rooms":         `max(ruleshift_room_runtimes)`,
		"connections":   `sum(ruleshift_gateway_connections)`,
		"command_rate":  `sum(rate(ruleshift_gateway_commands_total[5m]))`,
		"command_count": `sum(increase(ruleshift_gateway_commands_total[5m]))`,
		"error_ratio":   `sum(rate(ruleshift_gateway_commands_total{result=~"error|module_unavailable|timeout"}[5m])) / clamp_min(sum(rate(ruleshift_gateway_commands_total[5m])), 1)`,
		"command_p95":   `histogram_quantile(0.95, sum by (le) (rate(ruleshift_gateway_command_duration_seconds_bucket[5m])))`,
		"queue_ratio":   `max(ruleshift_room_queue_saturation_ratio)`,
		"slow_ratio":    `sum(rate(ruleshift_gateway_slow_consumers_total[5m])) / clamp_min(sum(rate(ruleshift_gateway_connections_opened_total[5m])), 1)`,
	}
	type result struct {
		sample Sample
		err    error
	}
	results := make(map[string]result, len(queries))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, query := range queries {
		name, query := name, query
		wg.Add(1)
		go func() {
			defer wg.Done()
			sample, err := h.prometheus.Query(ctx, query)
			mu.Lock()
			results[name] = result{sample: sample, err: err}
			mu.Unlock()
		}()
	}
	wg.Wait()

	now := time.Now().UTC()
	thresholds := h.cfg.Thresholds
	value := Overview{Status: "healthy", Reasons: []string{}, GeneratedAt: now, Thresholds: OverviewThresholds{StaleAfterSeconds: thresholds.StaleAfter.Seconds(), UnavailableAfterSeconds: thresholds.UnavailableAfter.Seconds(), ErrorRatio: thresholds.ErrorRatio, CommandP95Seconds: thresholds.CommandP95.Seconds(), QueueSaturationRatio: thresholds.QueueSaturation, SlowConsumerRatio: thresholds.SlowConsumerRate, MinCommands: thresholds.MinCommands}, Grafana: GrafanaLinks{SystemOverview: h.cfg.GrafanaSystem, RuntimeDiagnostics: h.cfg.GrafanaRuntime}}
	up := results["up"]
	upTimestamp := results["up_timestamp"]
	if up.err != nil || !up.sample.Present || upTimestamp.err != nil || !upTimestamp.sample.Present {
		value.Status = "unavailable"
		value.Reasons = append(value.Reasons, "metrics_unavailable")
		return value
	}
	seconds, fraction := mathModf(upTimestamp.sample.Value)
	value.SourceTimestamp = time.Unix(int64(seconds), int64(fraction*1e9)).UTC()
	age := now.Sub(value.SourceTimestamp)
	if up.sample.Value < 1 || age > thresholds.UnavailableAfter {
		value.Status = "unavailable"
		value.Reasons = append(value.Reasons, "ruleshift_unavailable")
		return value
	}
	if age > thresholds.StaleAfter {
		value.Status = "degraded"
		value.Reasons = append(value.Reasons, "metrics_stale")
	}
	value.Metrics = OverviewMetrics{ActiveRooms: sampleValue(results["rooms"]), Connections: sampleValue(results["connections"]), CommandRate: sampleValue(results["command_rate"]), ServerErrorRatio: sampleValue(results["error_ratio"]), CommandP95Seconds: sampleValue(results["command_p95"]), QueueSaturationRatio: sampleValue(results["queue_ratio"]), SlowConsumerRatio: sampleValue(results["slow_ratio"])}
	commandCount := sampleValue(results["command_count"])
	if commandCount >= thresholds.MinCommands && value.Metrics.ServerErrorRatio > thresholds.ErrorRatio {
		value.Status = "degraded"
		value.Reasons = append(value.Reasons, "server_error_ratio_high")
	}
	if commandCount >= thresholds.MinCommands && value.Metrics.CommandP95Seconds > thresholds.CommandP95.Seconds() {
		value.Status = "degraded"
		value.Reasons = append(value.Reasons, "command_latency_high")
	}
	if value.Metrics.QueueSaturationRatio >= thresholds.QueueSaturation {
		value.Status = "degraded"
		value.Reasons = append(value.Reasons, "room_queue_saturated")
	}
	if value.Metrics.SlowConsumerRatio > thresholds.SlowConsumerRate {
		value.Status = "degraded"
		value.Reasons = append(value.Reasons, "slow_consumers_high")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(h.cfg.OperationsURL, "/")+"/healthz", nil)
	if response, err := h.upstream.Do(request); err != nil || response.StatusCode != http.StatusOK {
		value.Status = "degraded"
		value.Reasons = append(value.Reasons, "room_diagnostics_unavailable")
		if response != nil {
			response.Body.Close()
		}
	} else {
		response.Body.Close()
	}
	return value
}

func sampleValue(value struct {
	sample Sample
	err    error
}) float64 {
	if value.err != nil || !value.sample.Present {
		return 0
	}
	return value.sample.Value
}

func (h *Handler) rooms(w http.ResponseWriter, r *http.Request) {
	allowed := []string{"cursor", "limit", "status", "module", "q"}
	query := url.Values{}
	for _, key := range allowed {
		if value := r.URL.Query().Get(key); value != "" {
			query.Set(key, value)
		}
	}
	h.proxy(w, r, "/internal/v1/rooms", query)
}

func (h *Handler) room(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("public_room_ref")
	if !operations.ValidPublicRoomRef(ref) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "room_not_found"})
		return
	}
	h.proxy(w, r, "/internal/v1/rooms/"+url.PathEscape(ref), nil)
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, path string, query url.Values) {
	target := strings.TrimRight(h.cfg.OperationsURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "upstream_error"})
		return
	}
	response, err := h.upstream.Do(request)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "room_diagnostics_unavailable"})
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUpstreamBody+1))
	if err != nil || len(body) > maxUpstreamBody {
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "invalid_upstream_response"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
