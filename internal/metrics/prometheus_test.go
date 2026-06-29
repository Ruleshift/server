package metrics

import (
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestTelemetryUsesOnlyBoundedLabels(t *testing.T) {
	telemetry := New()
	telemetry.ConnectionOpened()
	telemetry.Message("in", "command", "ok", 128)
	telemetry.Command("ok", 10*time.Millisecond)
	telemetry.RoomOperation("apply", "ok", 10*time.Millisecond, time.Millisecond, .25)
	families, err := telemetry.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "ruleshift_") {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				switch label.GetName() {
				case "room_id", "public_room_ref", "player_id", "developer_id", "trace_id", "module_id", "version":
					t.Fatalf("metric %s contains forbidden label %q", family.GetName(), label.GetName())
				}
			}
		}
	}
}

func TestRefreshRoomsDoesNotCreatePerRoomSeries(t *testing.T) {
	telemetry := New()
	telemetry.RefreshRooms(10, .25)
	first, err := telemetry.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	telemetry.RefreshRooms(1000, .9)
	second, err := telemetry.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if metricCount(first) != metricCount(second) {
		t.Fatalf("metric series grew with room count: %d -> %d", metricCount(first), metricCount(second))
	}
}

func metricCount(families []*dto.MetricFamily) int {
	total := 0
	for _, family := range families {
		total += len(family.Metric)
	}
	return total
}
