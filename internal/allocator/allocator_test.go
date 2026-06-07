package allocator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRegistryReservesServerCapacityAtomically(t *testing.T) {
	clock := newManualClock(time.Unix(100, 0).UTC())
	registry := newTestRegistry(t, clock)
	registerTestServer(t, registry, "server-1", 1, ServerStatusAvailable)

	const attempts = 16
	var wg sync.WaitGroup
	results := make(chan error, attempts)

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := registry.Reserve(context.Background(), ReservationRequest{
				GameID:  "game-1",
				BuildID: "build-1",
				MatchID: "match-" + string(rune('a'+i)),
				Seats:   1,
			})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrNoServerCapacity) {
			t.Fatalf("Reserve error = %v, want ErrNoServerCapacity", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful reservations = %d, want 1", successes)
	}

	server, err := registry.ServerSnapshot(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("ServerSnapshot returned error: %v", err)
	}
	if server.ReservedSeats != 1 {
		t.Fatalf("ReservedSeats = %d, want 1", server.ReservedSeats)
	}
}

func TestRegistryReserveIsIdempotentByMatch(t *testing.T) {
	clock := newManualClock(time.Unix(100, 0).UTC())
	registry := newTestRegistry(t, clock)
	registerTestServer(t, registry, "server-1", 2, ServerStatusAvailable)

	first, err := registry.Reserve(context.Background(), ReservationRequest{GameID: "game-1", BuildID: "build-1", MatchID: "match-1", Seats: 1})
	if err != nil {
		t.Fatalf("first Reserve returned error: %v", err)
	}
	second, err := registry.Reserve(context.Background(), ReservationRequest{GameID: "game-1", BuildID: "build-1", MatchID: "match-1", Seats: 1})
	if err != nil {
		t.Fatalf("second Reserve returned error: %v", err)
	}
	if first.ReservationID != second.ReservationID {
		t.Fatalf("reservation ids = %q/%q, want same id", first.ReservationID, second.ReservationID)
	}
}

func TestRegistryCleanupExpiredReleasesSeats(t *testing.T) {
	clock := newManualClock(time.Unix(100, 0).UTC())
	registry := newTestRegistry(t, clock)
	registerTestServer(t, registry, "server-1", 1, ServerStatusAvailable)

	if _, err := registry.Reserve(context.Background(), ReservationRequest{GameID: "game-1", BuildID: "build-1", MatchID: "match-1", Seats: 1}); err != nil {
		t.Fatalf("Reserve returned error: %v", err)
	}

	clock.advance(6 * time.Second)
	if expired := registry.CleanupExpired(context.Background(), clock.now()); expired != 1 {
		t.Fatalf("CleanupExpired = %d, want 1", expired)
	}

	server, err := registry.ServerSnapshot(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("ServerSnapshot returned error: %v", err)
	}
	if server.ReservedSeats != 0 {
		t.Fatalf("ReservedSeats = %d, want 0", server.ReservedSeats)
	}
}

func TestRegistryUnavailableServerCannotBeReserved(t *testing.T) {
	clock := newManualClock(time.Unix(100, 0).UTC())
	registry := newTestRegistry(t, clock)
	registerTestServer(t, registry, "server-1", 1, ServerStatusUnavailable)

	_, err := registry.Reserve(context.Background(), ReservationRequest{GameID: "game-1", BuildID: "build-1", MatchID: "match-1", Seats: 1})
	if !errors.Is(err, ErrNoServerCapacity) {
		t.Fatalf("Reserve error = %v, want ErrNoServerCapacity", err)
	}
}

func newTestRegistry(t *testing.T, clock *manualClock) *Registry {
	t.Helper()

	registry, err := NewRegistry(Config{
		ReservationTTL: 5 * time.Second,
		Clock:          clock.now,
	})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	return registry
}

func registerTestServer(t *testing.T, registry *Registry, serverID string, seats int, status ServerStatus) {
	t.Helper()

	if _, err := registry.RegisterServer(context.Background(), RegisterServerRequest{
		ServerID:      serverID,
		GameID:        "game-1",
		BuildID:       "build-1",
		Endpoint:      "127.0.0.1:7777",
		CapacitySeats: seats,
		Status:        status,
	}); err != nil {
		t.Fatalf("RegisterServer returned error: %v", err)
	}
}

type manualClock struct {
	mu      sync.Mutex
	current time.Time
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{current: now}
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *manualClock) advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(duration)
}
