package matchmaking

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/allocator"
	"github.com/Ruleshift/server/internal/connecttoken"
)

func TestServiceCreateTicketIsIdempotent(t *testing.T) {
	service, _ := newTestService(t, 2, 2, allocator.ServerStatusAvailable)

	req := CreateTicketRequest{
		GameID:         "game-1",
		BuildID:        "build-1",
		PlayerID:       "player-1",
		IdempotencyKey: "request-1",
	}
	first, err := service.CreateTicket(context.Background(), req)
	if err != nil {
		t.Fatalf("first CreateTicket returned error: %v", err)
	}
	second, err := service.CreateTicket(context.Background(), req)
	if err != nil {
		t.Fatalf("second CreateTicket returned error: %v", err)
	}
	if first.TicketID != second.TicketID {
		t.Fatalf("ticket ids = %q/%q, want idempotent id", first.TicketID, second.TicketID)
	}
	if first.State != StateQueued {
		t.Fatalf("ticket state = %s, want queued", first.State)
	}
}

func TestServiceCancelTicket(t *testing.T) {
	service, _ := newTestService(t, 2, 2, allocator.ServerStatusAvailable)

	ticket, err := service.CreateTicket(context.Background(), CreateTicketRequest{GameID: "game-1", BuildID: "build-1", PlayerID: "player-1"})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	canceled, err := service.CancelTicket(context.Background(), ticket.TicketID, "player-1")
	if err != nil {
		t.Fatalf("CancelTicket returned error: %v", err)
	}
	if canceled.State != StateCanceled {
		t.Fatalf("ticket state = %s, want canceled", canceled.State)
	}

	if matches, err := service.FormMatches(context.Background()); err != nil || len(matches) != 0 {
		t.Fatalf("FormMatches = %d, %v; want no matches from canceled ticket", len(matches), err)
	}
}

func TestServiceFormsMatchFromQueuedTickets(t *testing.T) {
	service, _ := newTestService(t, 2, 2, allocator.ServerStatusAvailable)
	createPlayers(t, service, "player-1", "player-2")

	matches, err := service.FormMatches(context.Background())
	if err != nil {
		t.Fatalf("FormMatches returned error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("match count = %d, want 1", len(matches))
	}
	match := matches[0]
	if match.State != StateAllocating {
		t.Fatalf("match state = %s, want allocating", match.State)
	}
	if len(match.PlayerIDs) != 2 || len(match.TicketIDs) != 2 {
		t.Fatalf("match players/tickets = %d/%d, want 2/2", len(match.PlayerIDs), len(match.TicketIDs))
	}
}

func TestServiceAssignsServerAndConnectTokens(t *testing.T) {
	service, registry := newTestService(t, 2, 2, allocator.ServerStatusAvailable)
	createPlayers(t, service, "player-1", "player-2")
	match := formOneMatch(t, service)

	assignments, err := service.AssignMatch(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("AssignMatch returned error: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("assignment count = %d, want 2", len(assignments))
	}
	if assignments[0].Endpoint != "127.0.0.1:7777" || assignments[0].ConnectToken == "" {
		t.Fatalf("assignment endpoint/token = %q/%t, want endpoint and token", assignments[0].Endpoint, assignments[0].ConnectToken != "")
	}

	server, err := registry.ServerSnapshot(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("ServerSnapshot returned error: %v", err)
	}
	if server.ReservedSeats != 2 {
		t.Fatalf("ReservedSeats = %d, want 2", server.ReservedSeats)
	}
}

func TestServiceValidatesConnectTokenAndMarksConnecting(t *testing.T) {
	service, _ := newTestService(t, 1, 1, allocator.ServerStatusAvailable)
	createPlayers(t, service, "player-1")
	match := formOneMatch(t, service)
	assignments, err := service.AssignMatch(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("AssignMatch returned error: %v", err)
	}

	assignment, err := service.ValidateConnectToken(context.Background(), assignments[0].ConnectToken)
	if err != nil {
		t.Fatalf("ValidateConnectToken returned error: %v", err)
	}
	if assignment.State != StateConnecting {
		t.Fatalf("assignment state = %s, want connecting", assignment.State)
	}
	updatedMatch, err := service.Match(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("Match returned error: %v", err)
	}
	if updatedMatch.State != StateConnecting {
		t.Fatalf("match state = %s, want connecting", updatedMatch.State)
	}
}

func TestServiceAssignmentTTLExpiresAndReleasesReservation(t *testing.T) {
	service, registry, clock := newTestServiceWithClock(t, 1, 1, allocator.ServerStatusAvailable)
	createPlayers(t, service, "player-1")
	match := formOneMatch(t, service)
	assignments, err := service.AssignMatch(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("AssignMatch returned error: %v", err)
	}

	clock.advance(6 * time.Second)
	expired, err := service.Expire(context.Background(), clock.now())
	if err != nil {
		t.Fatalf("Expire returned error: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired count = %d, want 1", expired)
	}

	assignment, err := service.Assignment(context.Background(), assignments[0].AssignmentID)
	if err != nil {
		t.Fatalf("Assignment returned error: %v", err)
	}
	if assignment.State != StateExpired {
		t.Fatalf("assignment state = %s, want expired", assignment.State)
	}
	server, err := registry.ServerSnapshot(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("ServerSnapshot returned error: %v", err)
	}
	if server.ReservedSeats != 0 {
		t.Fatalf("ReservedSeats = %d, want 0", server.ReservedSeats)
	}
	if _, err := service.ValidateConnectToken(context.Background(), assignments[0].ConnectToken); !errors.Is(err, connecttoken.ErrExpiredToken) {
		t.Fatalf("ValidateConnectToken error = %v, want ErrExpiredToken", err)
	}
}

func TestServiceAllocationFailsWhenServerUnavailable(t *testing.T) {
	service, _ := newTestService(t, 2, 2, allocator.ServerStatusUnavailable)
	createPlayers(t, service, "player-1", "player-2")
	match := formOneMatch(t, service)

	_, err := service.AssignMatch(context.Background(), match.MatchID)
	if !errors.Is(err, allocator.ErrNoServerCapacity) {
		t.Fatalf("AssignMatch error = %v, want ErrNoServerCapacity", err)
	}
	updatedMatch, err := service.Match(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("Match returned error: %v", err)
	}
	if updatedMatch.State != StateFailed {
		t.Fatalf("match state = %s, want failed", updatedMatch.State)
	}
}

func TestServiceFailServerFailsAssignedMatchAndReleasesReservation(t *testing.T) {
	service, registry := newTestService(t, 2, 2, allocator.ServerStatusAvailable)
	createPlayers(t, service, "player-1", "player-2")
	match := formOneMatch(t, service)
	if _, err := service.AssignMatch(context.Background(), match.MatchID); err != nil {
		t.Fatalf("AssignMatch returned error: %v", err)
	}

	if err := service.FailServer(context.Background(), "server-1", "heartbeat_lost"); err != nil {
		t.Fatalf("FailServer returned error: %v", err)
	}
	updatedMatch, err := service.Match(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("Match returned error: %v", err)
	}
	if updatedMatch.State != StateFailed {
		t.Fatalf("match state = %s, want failed", updatedMatch.State)
	}
	server, err := registry.ServerSnapshot(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("ServerSnapshot returned error: %v", err)
	}
	if server.ReservedSeats != 0 {
		t.Fatalf("ReservedSeats = %d, want 0", server.ReservedSeats)
	}
	if server.Status != allocator.ServerStatusUnavailable {
		t.Fatalf("server status = %s, want unavailable", server.Status)
	}
}

func TestServiceConcurrentAssignMatchIsIdempotent(t *testing.T) {
	service, registry := newTestService(t, 2, 2, allocator.ServerStatusAvailable)
	createPlayers(t, service, "player-1", "player-2")
	match := formOneMatch(t, service)

	const attempts = 8
	var wg sync.WaitGroup
	results := make(chan []Assignment, attempts)
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assignments, err := service.AssignMatch(context.Background(), match.MatchID)
			if err != nil {
				errs <- err
				return
			}
			results <- assignments
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	if err := <-errs; err != nil {
		t.Fatalf("AssignMatch returned error: %v", err)
	}

	var first []Assignment
	for assignments := range results {
		if len(assignments) != 2 {
			t.Fatalf("assignment count = %d, want 2", len(assignments))
		}
		if first == nil {
			first = assignments
			continue
		}
		if first[0].AssignmentID != assignments[0].AssignmentID || first[1].AssignmentID != assignments[1].AssignmentID {
			t.Fatalf("concurrent AssignMatch returned different assignments")
		}
	}

	server, err := registry.ServerSnapshot(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("ServerSnapshot returned error: %v", err)
	}
	if server.ReservedSeats != 2 {
		t.Fatalf("ReservedSeats = %d, want 2", server.ReservedSeats)
	}
}

func TestServiceHonorsCanceledContext(t *testing.T) {
	service, _ := newTestService(t, 2, 2, allocator.ServerStatusAvailable)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.CreateTicket(ctx, CreateTicketRequest{GameID: "game-1", BuildID: "build-1", PlayerID: "player-1"})
	if err == nil {
		t.Fatal("CreateTicket returned nil error, want canceled context error")
	}
}

func TestServiceMarksMatchInGameAndEnded(t *testing.T) {
	service, registry := newTestService(t, 1, 1, allocator.ServerStatusAvailable)
	createPlayers(t, service, "player-1")
	match := formOneMatch(t, service)
	assignments, err := service.AssignMatch(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("AssignMatch returned error: %v", err)
	}
	if _, err := service.ValidateConnectToken(context.Background(), assignments[0].ConnectToken); err != nil {
		t.Fatalf("ValidateConnectToken returned error: %v", err)
	}
	inGame, err := service.MarkInGame(context.Background(), match.MatchID)
	if err != nil {
		t.Fatalf("MarkInGame returned error: %v", err)
	}
	if inGame.State != StateInGame {
		t.Fatalf("match state = %s, want in_game", inGame.State)
	}
	ended, err := service.EndMatch(context.Background(), match.MatchID, "game_completed")
	if err != nil {
		t.Fatalf("EndMatch returned error: %v", err)
	}
	if ended.State != StateEnded {
		t.Fatalf("match state = %s, want ended", ended.State)
	}
	server, err := registry.ServerSnapshot(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("ServerSnapshot returned error: %v", err)
	}
	if server.ReservedSeats != 0 {
		t.Fatalf("ReservedSeats = %d, want 0", server.ReservedSeats)
	}
}

func createPlayers(t *testing.T, service *Service, playerIDs ...string) {
	t.Helper()

	for _, playerID := range playerIDs {
		if _, err := service.CreateTicket(context.Background(), CreateTicketRequest{
			GameID:   "game-1",
			BuildID:  "build-1",
			PlayerID: playerID,
		}); err != nil {
			t.Fatalf("CreateTicket(%s) returned error: %v", playerID, err)
		}
	}
}

func formOneMatch(t *testing.T, service *Service) Match {
	t.Helper()

	matches, err := service.FormMatches(context.Background())
	if err != nil {
		t.Fatalf("FormMatches returned error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("match count = %d, want 1", len(matches))
	}
	return matches[0]
}

func newTestService(t *testing.T, playersPerMatch int, serverSeats int, serverStatus allocator.ServerStatus) (*Service, *allocator.Registry) {
	t.Helper()

	service, registry, _ := newTestServiceWithClock(t, playersPerMatch, serverSeats, serverStatus)
	return service, registry
}

func newTestServiceWithClock(t *testing.T, playersPerMatch int, serverSeats int, serverStatus allocator.ServerStatus) (*Service, *allocator.Registry, *testClock) {
	t.Helper()

	clock := &testClock{current: time.Unix(100, 0).UTC()}
	registry, err := allocator.NewRegistry(allocator.Config{
		ReservationTTL: 5 * time.Second,
		Clock:          clock.now,
	})
	if err != nil {
		t.Fatalf("allocator.NewRegistry returned error: %v", err)
	}
	if _, err := registry.RegisterServer(context.Background(), allocator.RegisterServerRequest{
		ServerID:      "server-1",
		GameID:        "game-1",
		BuildID:       "build-1",
		Endpoint:      "127.0.0.1:7777",
		CapacitySeats: serverSeats,
		Status:        serverStatus,
	}); err != nil {
		t.Fatalf("RegisterServer returned error: %v", err)
	}

	tokens, err := connecttoken.NewManagerWithClock([]byte(strings.Repeat("s", 32)), clock.now)
	if err != nil {
		t.Fatalf("connecttoken.NewManagerWithClock returned error: %v", err)
	}
	service, err := NewService(Config{
		TicketTTL:       10 * time.Second,
		AssignmentTTL:   5 * time.Second,
		PlayersPerMatch: playersPerMatch,
		Clock:           clock.now,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, registry, tokens)
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	return service, registry, clock
}

type testClock struct {
	mu      sync.Mutex
	current time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *testClock) advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(duration)
}
