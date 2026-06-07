package matchmaking

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/allocator"
	"github.com/Ruleshift/server/internal/connecttoken"
)

func BenchmarkServiceCreateTicketsAndFormMatches(b *testing.B) {
	clock := &testClock{current: time.Unix(100, 0).UTC()}
	registry, err := allocator.NewRegistry(allocator.Config{
		ReservationTTL: 5 * time.Second,
		Clock:          clock.now,
	})
	if err != nil {
		b.Fatalf("allocator.NewRegistry returned error: %v", err)
	}
	if _, err := registry.RegisterServer(context.Background(), allocator.RegisterServerRequest{
		ServerID:      "server-1",
		GameID:        "game-1",
		BuildID:       "build-1",
		Endpoint:      "127.0.0.1:7777",
		CapacitySeats: (b.N + 1) * 2,
		Status:        allocator.ServerStatusAvailable,
	}); err != nil {
		b.Fatalf("RegisterServer returned error: %v", err)
	}
	tokens, err := connecttoken.NewManagerWithClock([]byte(strings.Repeat("s", 32)), clock.now)
	if err != nil {
		b.Fatalf("NewManagerWithClock returned error: %v", err)
	}
	service, err := NewService(Config{
		TicketTTL:       10 * time.Second,
		AssignmentTTL:   5 * time.Second,
		PlayersPerMatch: 2,
		Clock:           clock.now,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, registry, tokens)
	if err != nil {
		b.Fatalf("NewService returned error: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.CreateTicket(context.Background(), CreateTicketRequest{
			GameID:   "game-1",
			BuildID:  "build-1",
			PlayerID: fmt.Sprintf("player-%d-a", i),
		}); err != nil {
			b.Fatal(err)
		}
		if _, err := service.CreateTicket(context.Background(), CreateTicketRequest{
			GameID:   "game-1",
			BuildID:  "build-1",
			PlayerID: fmt.Sprintf("player-%d-b", i),
		}); err != nil {
			b.Fatal(err)
		}
		matches, err := service.FormMatches(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		if len(matches) != 1 {
			b.Fatalf("match count = %d, want 1", len(matches))
		}
	}
}
