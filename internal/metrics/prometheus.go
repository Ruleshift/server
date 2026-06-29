package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Recorder is the deliberately small metrics boundary used by subsystems that
// do not need labels. New hot-path instrumentation should prefer Observer's
// typed methods so metric names and label values stay bounded.
type Recorder interface {
	IncCounter(name string)
	SetGauge(name string, value float64)
	ObserveHistogram(name string, value float64)
}

// Observer records the bounded, game-agnostic signals used by the monitoring
// MVP. Implementations must not add room, player, developer, or trace IDs as
// labels.
type Observer interface {
	ConnectionOpened()
	ConnectionClosed()
	Message(direction, kind, result string, sizeBytes int)
	Command(result string, duration time.Duration)
	SlowConsumer()
	RoomOperation(operation, result string, duration, queueWait time.Duration, queueRatio float64)
	Revision(event string)
	Snapshot(reason, result string)
	Broadcast(recipients int, duration time.Duration)
}

type NopRecorder struct{}

func (NopRecorder) IncCounter(string)                   {}
func (NopRecorder) SetGauge(string, float64)            {}
func (NopRecorder) ObserveHistogram(string, float64)    {}
func (NopRecorder) ConnectionOpened()                   {}
func (NopRecorder) ConnectionClosed()                   {}
func (NopRecorder) Message(string, string, string, int) {}
func (NopRecorder) Command(string, time.Duration)       {}
func (NopRecorder) SlowConsumer()                       {}
func (NopRecorder) RoomOperation(string, string, time.Duration, time.Duration, float64) {
}
func (NopRecorder) Revision(string)              {}
func (NopRecorder) Snapshot(string, string)      {}
func (NopRecorder) Broadcast(int, time.Duration) {}

// Telemetry owns an isolated Prometheus registry. Tests and multiple gateway
// instances can therefore coexist without global collector collisions.
type Telemetry struct {
	registry *prometheus.Registry

	up                prometheus.Gauge
	roomRuntimes      prometheus.Gauge
	roomQueueMax      prometheus.Gauge
	connections       prometheus.Gauge
	connectionsOpened prometheus.Counter
	messages          *prometheus.CounterVec
	messageBytes      *prometheus.HistogramVec
	commands          *prometheus.CounterVec
	commandDuration   *prometheus.HistogramVec
	slowConsumers     prometheus.Counter
	roomOperations    *prometheus.CounterVec
	roomDuration      *prometheus.HistogramVec
	roomQueueWait     *prometheus.HistogramVec
	roomQueueRatio    *prometheus.HistogramVec
	revisions         *prometheus.CounterVec
	snapshots         *prometheus.CounterVec
	broadcastDuration prometheus.Histogram
	broadcastTargets  prometheus.Histogram
}

func New() *Telemetry {
	latencyBuckets := []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
	t := &Telemetry{
		registry:          prometheus.NewRegistry(),
		up:                prometheus.NewGauge(prometheus.GaugeOpts{Name: "ruleshift_up", Help: "Whether the Ruleshift gateway process is running."}),
		roomRuntimes:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "ruleshift_room_runtimes", Help: "Number of active authoritative room runtimes."}),
		roomQueueMax:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "ruleshift_room_queue_saturation_ratio", Help: "Maximum current room input queue saturation ratio."}),
		connections:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "ruleshift_gateway_connections", Help: "Current WebSocket connections."}),
		connectionsOpened: prometheus.NewCounter(prometheus.CounterOpts{Name: "ruleshift_gateway_connections_opened_total", Help: "WebSocket connections accepted by the gateway."}),
		messages:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ruleshift_gateway_messages_total", Help: "Gateway protobuf messages by bounded direction, type, and result."}, []string{"direction", "type", "result"}),
		messageBytes:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ruleshift_gateway_message_size_bytes", Help: "Gateway protobuf message sizes in bytes.", Buckets: prometheus.ExponentialBuckets(64, 2, 11)}, []string{"direction", "type"}),
		commands:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ruleshift_gateway_commands_total", Help: "Game commands handled by result."}, []string{"result"}),
		commandDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ruleshift_gateway_command_duration_seconds", Help: "Server-side command-to-enqueue duration in seconds.", Buckets: latencyBuckets}, []string{"result"}),
		slowConsumers:     prometheus.NewCounter(prometheus.CounterOpts{Name: "ruleshift_gateway_slow_consumers_total", Help: "Outbound messages dropped because a bounded session queue was full."}),
		roomOperations:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ruleshift_room_operations_total", Help: "Authoritative room operations by bounded operation and result."}, []string{"operation", "result"}),
		roomDuration:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ruleshift_room_operation_duration_seconds", Help: "Authoritative room operation duration in seconds.", Buckets: latencyBuckets}, []string{"operation", "result"}),
		roomQueueWait:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ruleshift_room_queue_wait_duration_seconds", Help: "Time spent waiting for the sequential room runtime.", Buckets: latencyBuckets}, []string{"operation"}),
		roomQueueRatio:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "ruleshift_room_queue_observed_saturation_ratio", Help: "Observed room queue saturation ratio when submitting operations.", Buckets: []float64{0, .1, .25, .5, .75, .9, 1}}, []string{"operation"}),
		revisions:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ruleshift_room_revisions_total", Help: "Committed authoritative room revisions by event kind."}, []string{"event"}),
		snapshots:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ruleshift_room_snapshots_total", Help: "Room snapshots by reason and result."}, []string{"reason", "result"}),
		broadcastDuration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "ruleshift_gateway_broadcast_duration_seconds", Help: "Duration of a room broadcast in seconds.", Buckets: latencyBuckets}),
		broadcastTargets:  prometheus.NewHistogram(prometheus.HistogramOpts{Name: "ruleshift_gateway_broadcast_recipients", Help: "Recipients targeted by a room broadcast.", Buckets: prometheus.ExponentialBuckets(1, 2, 10)}),
	}
	t.registry.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		t.up, t.roomRuntimes, t.roomQueueMax, t.connections, t.connectionsOpened,
		t.messages, t.messageBytes, t.commands, t.commandDuration, t.slowConsumers,
		t.roomOperations, t.roomDuration, t.roomQueueWait, t.roomQueueRatio,
		t.revisions, t.snapshots, t.broadcastDuration, t.broadcastTargets,
	)
	t.up.Set(1)
	return t
}

func (t *Telemetry) Handler() http.Handler {
	return promhttp.HandlerFor(t.registry, promhttp.HandlerOpts{})
}

func (t *Telemetry) RefreshRooms(count int, maxQueueRatio float64) {
	t.roomRuntimes.Set(float64(count))
	t.roomQueueMax.Set(maxQueueRatio)
}

func (t *Telemetry) ConnectionOpened() {
	t.connections.Inc()
	t.connectionsOpened.Inc()
}

func (t *Telemetry) ConnectionClosed() { t.connections.Dec() }

func (t *Telemetry) Message(direction, kind, result string, sizeBytes int) {
	t.messages.WithLabelValues(direction, kind, result).Inc()
	if sizeBytes >= 0 {
		t.messageBytes.WithLabelValues(direction, kind).Observe(float64(sizeBytes))
	}
}

func (t *Telemetry) Command(result string, duration time.Duration) {
	t.commands.WithLabelValues(result).Inc()
	t.commandDuration.WithLabelValues(result).Observe(duration.Seconds())
}

func (t *Telemetry) SlowConsumer() { t.slowConsumers.Inc() }

func (t *Telemetry) RoomOperation(operation, result string, duration, queueWait time.Duration, queueRatio float64) {
	t.roomOperations.WithLabelValues(operation, result).Inc()
	t.roomDuration.WithLabelValues(operation, result).Observe(duration.Seconds())
	t.roomQueueWait.WithLabelValues(operation).Observe(queueWait.Seconds())
	t.roomQueueRatio.WithLabelValues(operation).Observe(queueRatio)
}

func (t *Telemetry) Revision(event string) { t.revisions.WithLabelValues(event).Inc() }
func (t *Telemetry) Snapshot(reason, result string) {
	t.snapshots.WithLabelValues(reason, result).Inc()
}

func (t *Telemetry) Broadcast(recipients int, duration time.Duration) {
	t.broadcastTargets.Observe(float64(recipients))
	t.broadcastDuration.Observe(duration.Seconds())
}

var _ Recorder = NopRecorder{}
var _ Observer = NopRecorder{}
var _ Observer = (*Telemetry)(nil)
