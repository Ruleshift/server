package allocator

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func BenchmarkRegistryReserve(b *testing.B) {
	clock := newManualClock(time.Unix(100, 0).UTC())
	registry, err := NewRegistry(Config{
		ReservationTTL: 5 * time.Second,
		Clock:          clock.now,
	})
	if err != nil {
		b.Fatalf("NewRegistry returned error: %v", err)
	}
	if _, err := registry.RegisterServer(context.Background(), RegisterServerRequest{
		ServerID:      "server-1",
		GameID:        "game-1",
		BuildID:       "build-1",
		Endpoint:      "127.0.0.1:7777",
		CapacitySeats: b.N + 1,
		Status:        ServerStatusAvailable,
	}); err != nil {
		b.Fatalf("RegisterServer returned error: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := registry.Reserve(context.Background(), ReservationRequest{
			GameID:  "game-1",
			BuildID: "build-1",
			MatchID: "match-" + strconv.Itoa(i),
			Seats:   1,
		}); err != nil {
			b.Fatal(err)
		}
	}
}
