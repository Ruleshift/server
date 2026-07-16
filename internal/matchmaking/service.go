package matchmaking

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/allocator"
	"github.com/Ruleshift/server/internal/connecttoken"
	"github.com/Ruleshift/server/internal/metrics"
	"github.com/google/uuid"
)

type LifecycleState string

const (
	StateQueued     LifecycleState = "queued"
	StateMatched    LifecycleState = "matched"
	StateAllocating LifecycleState = "allocating"
	StateAssigned   LifecycleState = "assigned"
	StateConnecting LifecycleState = "connecting"
	StateInGame     LifecycleState = "in_game"
	StateEnded      LifecycleState = "ended"
	StateFailed     LifecycleState = "failed"
	StateCanceled   LifecycleState = "canceled"
	StateExpired    LifecycleState = "expired"
)

var (
	ErrInvalidRequest    = errors.New("invalid matchmaking request")
	ErrNotFound          = errors.New("matchmaking entity not found")
	ErrInvalidTransition = errors.New("invalid lifecycle transition")
	ErrTokenMismatch     = errors.New("connect token does not match assignment")
)

type Config struct {
	TicketTTL       time.Duration
	AssignmentTTL   time.Duration
	PlayersPerMatch int
	Clock           func() time.Time
	Logger          *slog.Logger
	Metrics         metrics.Recorder
}

type Service struct {
	mu                       sync.Mutex
	cfg                      Config
	allocator                *allocator.Registry
	tokens                   *connecttoken.Manager
	tickets                  map[string]*Ticket
	matches                  map[string]*Match
	assignments              map[string]*Assignment
	queues                   map[string][]string
	ticketByIdempotency      map[string]string
	activeTicketByPlayerPool map[string]string
	events                   []Event
	nextEventSequence        uint64
}

type CreateTicketRequest struct {
	GameID         string
	BuildID        string
	PlayerID       string
	IdempotencyKey string
	TTL            time.Duration
}

type Ticket struct {
	TicketID       string
	GameID         string
	BuildID        string
	PlayerID       string
	State          LifecycleState
	MatchID        string
	AssignmentID   string
	FailureReason  string
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      time.Time
}

type Match struct {
	MatchID       string
	GameID        string
	BuildID       string
	PlayerIDs     []string
	TicketIDs     []string
	AssignmentIDs []string
	State         LifecycleState
	ServerID      string
	Endpoint      string
	ReservationID string
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Assignment struct {
	AssignmentID  string
	TicketID      string
	MatchID       string
	ServerID      string
	Endpoint      string
	PlayerID      string
	State         LifecycleState
	ConnectToken  string
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	FailureReason string
}

type Event struct {
	Sequence   uint64
	EntityType string
	EntityID   string
	TicketID   string
	MatchID    string
	ServerID   string
	PlayerID   string
	State      LifecycleState
	Reason     string
	OccurredAt time.Time
}

func NewService(cfg Config, registry *allocator.Registry, tokenManager *connecttoken.Manager) (*Service, error) {
	if cfg.TicketTTL <= 0 {
		return nil, fmt.Errorf("ticket TTL must be positive")
	}
	if cfg.AssignmentTTL <= 0 {
		return nil, fmt.Errorf("assignment TTL must be positive")
	}
	if cfg.PlayersPerMatch <= 0 {
		return nil, fmt.Errorf("players per match must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time {
			return time.Now().UTC()
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NopRecorder{}
	}
	if registry == nil {
		return nil, fmt.Errorf("allocator registry must not be nil")
	}
	if tokenManager == nil {
		return nil, fmt.Errorf("connect token manager must not be nil")
	}

	return &Service{
		cfg:                      cfg,
		allocator:                registry,
		tokens:                   tokenManager,
		tickets:                  make(map[string]*Ticket),
		matches:                  make(map[string]*Match),
		assignments:              make(map[string]*Assignment),
		queues:                   make(map[string][]string),
		ticketByIdempotency:      make(map[string]string),
		activeTicketByPlayerPool: make(map[string]string),
		events:                   make([]Event, 0),
	}, nil
}

func (s *Service) CreateTicket(ctx context.Context, req CreateTicketRequest) (Ticket, error) {
	if err := ctx.Err(); err != nil {
		return Ticket{}, fmt.Errorf("create ticket: %w", err)
	}
	if req.GameID == "" || req.BuildID == "" || req.PlayerID == "" {
		return Ticket{}, ErrInvalidRequest
	}
	ttl := req.TTL
	if ttl == 0 {
		ttl = s.cfg.TicketTTL
	}
	if ttl <= 0 {
		return Ticket{}, fmt.Errorf("%w: ticket TTL must be positive", ErrInvalidRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Clock()
	s.expireQueuedTicketsLocked(now)

	if req.IdempotencyKey != "" {
		key := idempotencyKey(req)
		if ticketID := s.ticketByIdempotency[key]; ticketID != "" {
			if ticket := s.tickets[ticketID]; ticket != nil {
				return copyTicket(ticket), nil
			}
		}
	}

	activeKey := playerPoolKey(req.GameID, req.BuildID, req.PlayerID)
	if ticketID := s.activeTicketByPlayerPool[activeKey]; ticketID != "" {
		if ticket := s.tickets[ticketID]; ticket != nil && !isTerminal(ticket.State) {
			return copyTicket(ticket), nil
		}
	}

	id, err := uuid.NewRandom()
	if err != nil {
		return Ticket{}, fmt.Errorf("generate ticket id: %w", err)
	}
	ticketID := "ticket_" + id.String()
	ticket := &Ticket{
		TicketID:       ticketID,
		GameID:         req.GameID,
		BuildID:        req.BuildID,
		PlayerID:       req.PlayerID,
		State:          StateQueued,
		IdempotencyKey: req.IdempotencyKey,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(ttl),
	}
	s.tickets[ticket.TicketID] = ticket
	s.queues[poolKey(ticket.GameID, ticket.BuildID)] = append(s.queues[poolKey(ticket.GameID, ticket.BuildID)], ticket.TicketID)
	s.activeTicketByPlayerPool[activeKey] = ticket.TicketID
	if req.IdempotencyKey != "" {
		s.ticketByIdempotency[idempotencyKey(req)] = ticket.TicketID
	}
	s.recordEventLocked(Event{
		EntityType: "ticket",
		EntityID:   ticket.TicketID,
		TicketID:   ticket.TicketID,
		PlayerID:   ticket.PlayerID,
		State:      StateQueued,
		OccurredAt: now,
	})
	s.cfg.Metrics.IncCounter("matchmaking_ticket_created_total")
	s.cfg.Logger.Info("matchmaking ticket created")
	return copyTicket(ticket), nil
}

func (s *Service) CancelTicket(ctx context.Context, ticketID string, playerID string) (Ticket, error) {
	if err := ctx.Err(); err != nil {
		return Ticket{}, fmt.Errorf("cancel ticket: %w", err)
	}
	if ticketID == "" || playerID == "" {
		return Ticket{}, ErrInvalidRequest
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Clock()
	ticket := s.tickets[ticketID]
	if ticket == nil || ticket.PlayerID != playerID {
		return Ticket{}, ErrNotFound
	}
	if ticket.State != StateQueued {
		return Ticket{}, fmt.Errorf("%w: ticket %s is %s", ErrInvalidTransition, ticketID, ticket.State)
	}
	if err := s.setTicketStateLocked(ticket, StateCanceled, "player_canceled", now); err != nil {
		return Ticket{}, err
	}
	s.compactQueueLocked(poolKey(ticket.GameID, ticket.BuildID))
	s.cfg.Metrics.IncCounter("matchmaking_ticket_canceled_total")
	s.cfg.Logger.Info("matchmaking ticket canceled")
	return copyTicket(ticket), nil
}

func (s *Service) FormMatches(ctx context.Context) ([]Match, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("form matches: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Clock()
	s.expireQueuedTicketsLocked(now)

	matches := make([]Match, 0)
	for key := range s.queues {
		for {
			ticketIDs := s.readyTicketBatchLocked(key, s.cfg.PlayersPerMatch, now)
			if len(ticketIDs) < s.cfg.PlayersPerMatch {
				break
			}

			id, err := uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("generate match id: %w", err)
			}
			matchID := "match_" + id.String()
			first := s.tickets[ticketIDs[0]]
			match := &Match{
				MatchID:   matchID,
				GameID:    first.GameID,
				BuildID:   first.BuildID,
				State:     StateMatched,
				PlayerIDs: make([]string, 0, len(ticketIDs)),
				TicketIDs: make([]string, 0, len(ticketIDs)),
				CreatedAt: now,
				UpdatedAt: now,
			}
			s.matches[match.MatchID] = match
			s.recordEventLocked(Event{
				EntityType: "match",
				EntityID:   match.MatchID,
				MatchID:    match.MatchID,
				State:      StateMatched,
				OccurredAt: now,
			})

			for _, ticketID := range ticketIDs {
				ticket := s.tickets[ticketID]
				if err := s.setTicketStateLocked(ticket, StateMatched, "match_formed", now); err != nil {
					return nil, err
				}
				ticket.MatchID = match.MatchID
				match.TicketIDs = append(match.TicketIDs, ticket.TicketID)
				match.PlayerIDs = append(match.PlayerIDs, ticket.PlayerID)
			}
			if err := s.setMatchStateLocked(match, StateAllocating, "server_allocation_started", now); err != nil {
				return nil, err
			}
			for _, ticketID := range ticketIDs {
				if err := s.setTicketStateLocked(s.tickets[ticketID], StateAllocating, "server_allocation_started", now); err != nil {
					return nil, err
				}
			}
			s.compactQueueLocked(key)
			matches = append(matches, copyMatch(match))
			s.cfg.Metrics.IncCounter("matchmaking_match_formed_total")
			s.cfg.Logger.Info("match formed", "player_count", len(match.PlayerIDs))
		}
	}
	return matches, nil
}

func (s *Service) AssignMatch(ctx context.Context, matchID string) ([]Assignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("assign match: %w", err)
	}
	if matchID == "" {
		return nil, ErrInvalidRequest
	}

	match, err := s.matchForAllocation(matchID)
	if err != nil {
		return nil, err
	}
	if match.State == StateAssigned {
		s.mu.Lock()
		current := s.matches[match.MatchID]
		assignments := s.assignmentsForMatchLocked(current)
		s.mu.Unlock()
		return assignments, nil
	}

	reservation, err := s.allocator.Reserve(ctx, allocator.ReservationRequest{
		GameID:  match.GameID,
		BuildID: match.BuildID,
		MatchID: match.MatchID,
		Seats:   len(match.PlayerIDs),
	})
	if err != nil {
		s.failMatchAfterAllocationError(match.MatchID, "server_allocation_failed")
		return nil, fmt.Errorf("allocate server for match %s: %w", match.MatchID, err)
	}

	now := s.cfg.Clock()
	expiresAt := now.Add(s.cfg.AssignmentTTL)
	assignments := make([]Assignment, 0, len(match.PlayerIDs))
	for i, playerID := range match.PlayerIDs {
		id, err := uuid.NewRandom()
		if err != nil {
			_ = s.allocator.Release(context.Background(), reservation.ReservationID)
			s.failMatchAfterAllocationError(match.MatchID, "assignment_id_generation_failed")
			return nil, fmt.Errorf("generate assignment id: %w", err)
		}
		assignmentID := "assignment_" + id.String()
		token, err := s.tokens.Generate(connecttoken.Claims{
			AssignmentID: assignmentID,
			MatchID:      match.MatchID,
			ServerID:     reservation.ServerID,
			PlayerID:     playerID,
			ExpiresAt:    expiresAt,
		})
		if err != nil {
			_ = s.allocator.Release(context.Background(), reservation.ReservationID)
			s.failMatchAfterAllocationError(match.MatchID, "connect_token_generation_failed")
			return nil, err
		}
		assignments = append(assignments, Assignment{
			AssignmentID: assignmentID,
			TicketID:     match.TicketIDs[i],
			MatchID:      match.MatchID,
			ServerID:     reservation.ServerID,
			Endpoint:     reservation.Endpoint,
			PlayerID:     playerID,
			State:        StateAssigned,
			ConnectToken: token,
			ExpiresAt:    expiresAt,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.matches[match.MatchID]
	if current == nil {
		_ = s.allocator.Release(context.Background(), reservation.ReservationID)
		return nil, ErrNotFound
	}
	if current.State == StateAssigned {
		return s.assignmentsForMatchLocked(current), nil
	}
	if current.State != StateAllocating {
		_ = s.allocator.Release(context.Background(), reservation.ReservationID)
		return nil, fmt.Errorf("%w: match %s is %s", ErrInvalidTransition, current.MatchID, current.State)
	}

	current.ServerID = reservation.ServerID
	current.Endpoint = reservation.Endpoint
	current.ReservationID = reservation.ReservationID
	current.AssignmentIDs = current.AssignmentIDs[:0]
	for i := range assignments {
		assignment := assignments[i]
		s.assignments[assignment.AssignmentID] = &assignment
		current.AssignmentIDs = append(current.AssignmentIDs, assignment.AssignmentID)
		ticket := s.tickets[assignment.TicketID]
		ticket.AssignmentID = assignment.AssignmentID
		if err := s.setTicketStateLocked(ticket, StateAssigned, "assignment_created", now); err != nil {
			return nil, err
		}
		s.recordEventLocked(Event{
			EntityType: "assignment",
			EntityID:   assignment.AssignmentID,
			TicketID:   assignment.TicketID,
			MatchID:    assignment.MatchID,
			ServerID:   assignment.ServerID,
			PlayerID:   assignment.PlayerID,
			State:      StateAssigned,
			OccurredAt: now,
		})
	}
	if err := s.setMatchStateLocked(current, StateAssigned, "assignment_created", now); err != nil {
		return nil, err
	}
	s.cfg.Metrics.IncCounter("matchmaking_match_assigned_total")
	s.cfg.Logger.Info("match assigned", "assignment_count", len(current.AssignmentIDs))
	return s.assignmentsForMatchLocked(current), nil
}

func (s *Service) ValidateConnectToken(ctx context.Context, token string) (Assignment, error) {
	if err := ctx.Err(); err != nil {
		return Assignment{}, fmt.Errorf("validate connect token: %w", err)
	}

	claims, err := s.tokens.Validate(token)
	if err != nil {
		return Assignment{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Clock()
	assignment := s.assignments[claims.AssignmentID]
	if assignment == nil {
		return Assignment{}, ErrNotFound
	}
	if assignment.MatchID != claims.MatchID || assignment.ServerID != claims.ServerID || assignment.PlayerID != claims.PlayerID {
		return Assignment{}, ErrTokenMismatch
	}
	if subtle.ConstantTimeCompare([]byte(assignment.ConnectToken), []byte(token)) != 1 {
		return Assignment{}, ErrTokenMismatch
	}
	if !assignment.ExpiresAt.After(now) {
		return Assignment{}, connecttoken.ErrExpiredToken
	}
	if assignment.State == StateConnecting || assignment.State == StateInGame {
		return copyAssignment(assignment), nil
	}
	if assignment.State != StateAssigned {
		return Assignment{}, fmt.Errorf("%w: assignment %s is %s", ErrInvalidTransition, assignment.AssignmentID, assignment.State)
	}

	if err := s.setAssignmentStateLocked(assignment, StateConnecting, "connect_token_validated", now); err != nil {
		return Assignment{}, err
	}
	if ticket := s.tickets[assignment.TicketID]; ticket != nil && ticket.State == StateAssigned {
		if err := s.setTicketStateLocked(ticket, StateConnecting, "connect_token_validated", now); err != nil {
			return Assignment{}, err
		}
	}
	if match := s.matches[assignment.MatchID]; match != nil && match.State == StateAssigned {
		if err := s.setMatchStateLocked(match, StateConnecting, "player_connecting", now); err != nil {
			return Assignment{}, err
		}
	}
	s.cfg.Metrics.IncCounter("matchmaking_connect_token_valid_total")
	s.cfg.Logger.Info("connect token validated")
	return copyAssignment(assignment), nil
}

func (s *Service) MarkInGame(ctx context.Context, matchID string) (Match, error) {
	if err := ctx.Err(); err != nil {
		return Match{}, fmt.Errorf("mark match in-game: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Clock()
	match := s.matches[matchID]
	if match == nil {
		return Match{}, ErrNotFound
	}
	if match.State == StateInGame {
		return copyMatch(match), nil
	}
	if match.State != StateConnecting && match.State != StateAssigned {
		return Match{}, fmt.Errorf("%w: match %s is %s", ErrInvalidTransition, match.MatchID, match.State)
	}

	for _, assignmentID := range match.AssignmentIDs {
		assignment := s.assignments[assignmentID]
		if assignment != nil && (assignment.State == StateAssigned || assignment.State == StateConnecting) {
			if err := s.setAssignmentStateLocked(assignment, StateInGame, "game_server_accepted", now); err != nil {
				return Match{}, err
			}
		}
	}
	for _, ticketID := range match.TicketIDs {
		ticket := s.tickets[ticketID]
		if ticket != nil && (ticket.State == StateAssigned || ticket.State == StateConnecting) {
			if err := s.setTicketStateLocked(ticket, StateInGame, "game_server_accepted", now); err != nil {
				return Match{}, err
			}
		}
	}
	if err := s.setMatchStateLocked(match, StateInGame, "game_server_accepted", now); err != nil {
		return Match{}, err
	}
	s.cfg.Metrics.IncCounter("matchmaking_match_in_game_total")
	return copyMatch(match), nil
}

func (s *Service) EndMatch(ctx context.Context, matchID string, reason string) (Match, error) {
	if err := ctx.Err(); err != nil {
		return Match{}, fmt.Errorf("end match: %w", err)
	}

	reservationID := ""
	var ended Match
	s.mu.Lock()
	now := s.cfg.Clock()
	match := s.matches[matchID]
	if match == nil {
		s.mu.Unlock()
		return Match{}, ErrNotFound
	}
	if isTerminal(match.State) {
		ended = copyMatch(match)
		s.mu.Unlock()
		return ended, nil
	}
	reservationID = match.ReservationID
	if err := s.endMatchLocked(match, StateEnded, reason, now); err != nil {
		s.mu.Unlock()
		return Match{}, err
	}
	ended = copyMatch(match)
	s.mu.Unlock()

	if reservationID != "" {
		_ = s.allocator.Release(ctx, reservationID)
	}
	return ended, nil
}

func (s *Service) FailServer(ctx context.Context, serverID string, reason string) error {
	reservations, err := s.allocator.SetStatus(ctx, serverID, allocator.ServerStatusUnavailable)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Clock()
	for _, reservation := range reservations {
		match := s.matches[reservation.MatchID]
		if match == nil || isTerminal(match.State) {
			continue
		}
		if err := s.endMatchLocked(match, StateFailed, reason, now); err != nil {
			return err
		}
	}
	s.cfg.Metrics.IncCounter("matchmaking_server_failed_total")
	return nil
}

func (s *Service) Expire(ctx context.Context, now time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("expire matchmaking entities: %w", err)
	}
	if now.IsZero() {
		now = s.cfg.Clock()
	}

	reservationsToRelease := make([]string, 0)
	expired := 0

	s.mu.Lock()
	expired += s.expireQueuedTicketsLocked(now)
	for _, assignment := range s.assignments {
		if assignment == nil || isTerminal(assignment.State) || assignment.ExpiresAt.After(now) {
			continue
		}
		match := s.matches[assignment.MatchID]
		if match == nil || isTerminal(match.State) {
			continue
		}
		if match.ReservationID != "" {
			reservationsToRelease = append(reservationsToRelease, match.ReservationID)
		}
		if err := s.expireMatchLocked(match, assignment.AssignmentID, now); err != nil {
			s.mu.Unlock()
			return expired, err
		}
		expired++
	}
	s.mu.Unlock()

	for _, reservationID := range reservationsToRelease {
		_ = s.allocator.Release(ctx, reservationID)
	}
	if expired > 0 {
		s.cfg.Metrics.IncCounter("matchmaking_expired_total")
	}
	return expired, nil
}

func (s *Service) Ticket(ctx context.Context, ticketID string) (Ticket, error) {
	if err := ctx.Err(); err != nil {
		return Ticket{}, fmt.Errorf("get ticket: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket := s.tickets[ticketID]
	if ticket == nil {
		return Ticket{}, ErrNotFound
	}
	return copyTicket(ticket), nil
}

func (s *Service) Match(ctx context.Context, matchID string) (Match, error) {
	if err := ctx.Err(); err != nil {
		return Match{}, fmt.Errorf("get match: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	match := s.matches[matchID]
	if match == nil {
		return Match{}, ErrNotFound
	}
	return copyMatch(match), nil
}

func (s *Service) Assignment(ctx context.Context, assignmentID string) (Assignment, error) {
	if err := ctx.Err(); err != nil {
		return Assignment{}, fmt.Errorf("get assignment: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assignment := s.assignments[assignmentID]
	if assignment == nil {
		return Assignment{}, ErrNotFound
	}
	return copyAssignment(assignment), nil
}

func (s *Service) ListEvents(ctx context.Context, matchID string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list matchmaking events: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]Event, 0, len(s.events))
	for _, event := range s.events {
		if matchID == "" || event.MatchID == matchID {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *Service) matchForAllocation(matchID string) (Match, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	match := s.matches[matchID]
	if match == nil {
		return Match{}, ErrNotFound
	}
	if match.State == StateAssigned {
		return copyMatch(match), nil
	}
	if match.State != StateAllocating {
		return Match{}, fmt.Errorf("%w: match %s is %s", ErrInvalidTransition, match.MatchID, match.State)
	}
	return copyMatch(match), nil
}

func (s *Service) failMatchAfterAllocationError(matchID string, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	match := s.matches[matchID]
	if match == nil || isTerminal(match.State) {
		return
	}
	_ = s.endMatchLocked(match, StateFailed, reason, s.cfg.Clock())
}

func (s *Service) readyTicketBatchLocked(key string, count int, now time.Time) []string {
	queue := s.queues[key]
	if len(queue) < count {
		return nil
	}

	ticketIDs := make([]string, 0, count)
	for _, ticketID := range queue {
		ticket := s.tickets[ticketID]
		if ticket == nil || ticket.State != StateQueued || !ticket.ExpiresAt.After(now) {
			continue
		}
		ticketIDs = append(ticketIDs, ticketID)
		if len(ticketIDs) == count {
			return ticketIDs
		}
	}
	return nil
}

func (s *Service) expireQueuedTicketsLocked(now time.Time) int {
	expired := 0
	for _, queue := range s.queues {
		for _, ticketID := range queue {
			ticket := s.tickets[ticketID]
			if ticket == nil || ticket.State != StateQueued || ticket.ExpiresAt.After(now) {
				continue
			}
			_ = s.setTicketStateLocked(ticket, StateExpired, "ticket_ttl_expired", now)
			expired++
		}
	}
	for key := range s.queues {
		s.compactQueueLocked(key)
	}
	return expired
}

func (s *Service) expireMatchLocked(match *Match, expiredAssignmentID string, now time.Time) error {
	for _, assignmentID := range match.AssignmentIDs {
		assignment := s.assignments[assignmentID]
		if assignment == nil || isTerminal(assignment.State) {
			continue
		}
		next := StateFailed
		reason := "match_assignment_expired"
		if assignment.AssignmentID == expiredAssignmentID {
			next = StateExpired
			reason = "assignment_ttl_expired"
		}
		if err := s.setAssignmentStateLocked(assignment, next, reason, now); err != nil {
			return err
		}
	}
	for _, ticketID := range match.TicketIDs {
		ticket := s.tickets[ticketID]
		if ticket == nil || isTerminal(ticket.State) {
			continue
		}
		next := StateFailed
		reason := "match_assignment_expired"
		if ticket.AssignmentID == expiredAssignmentID {
			next = StateExpired
			reason = "assignment_ttl_expired"
		}
		if err := s.setTicketStateLocked(ticket, next, reason, now); err != nil {
			return err
		}
	}
	return s.setMatchStateLocked(match, StateFailed, "assignment_ttl_expired", now)
}

func (s *Service) endMatchLocked(match *Match, next LifecycleState, reason string, now time.Time) error {
	for _, assignmentID := range match.AssignmentIDs {
		assignment := s.assignments[assignmentID]
		if assignment != nil && !isTerminal(assignment.State) {
			if err := s.setAssignmentStateLocked(assignment, next, reason, now); err != nil {
				return err
			}
		}
	}
	for _, ticketID := range match.TicketIDs {
		ticket := s.tickets[ticketID]
		if ticket != nil && !isTerminal(ticket.State) {
			if err := s.setTicketStateLocked(ticket, next, reason, now); err != nil {
				return err
			}
		}
	}
	return s.setMatchStateLocked(match, next, reason, now)
}

func (s *Service) setTicketStateLocked(ticket *Ticket, next LifecycleState, reason string, now time.Time) error {
	if ticket == nil {
		return ErrNotFound
	}
	if ticket.State == next {
		return nil
	}
	if !canTransition(ticket.State, next) {
		return fmt.Errorf("%w: ticket %s %s -> %s", ErrInvalidTransition, ticket.TicketID, ticket.State, next)
	}
	ticket.State = next
	ticket.UpdatedAt = now
	if next == StateFailed {
		ticket.FailureReason = reason
	}
	if isTerminal(next) {
		delete(s.activeTicketByPlayerPool, playerPoolKey(ticket.GameID, ticket.BuildID, ticket.PlayerID))
	}
	s.recordEventLocked(Event{
		EntityType: "ticket",
		EntityID:   ticket.TicketID,
		TicketID:   ticket.TicketID,
		MatchID:    ticket.MatchID,
		PlayerID:   ticket.PlayerID,
		State:      next,
		Reason:     reason,
		OccurredAt: now,
	})
	return nil
}

func (s *Service) setMatchStateLocked(match *Match, next LifecycleState, reason string, now time.Time) error {
	if match == nil {
		return ErrNotFound
	}
	if match.State == next {
		return nil
	}
	if !canTransition(match.State, next) {
		return fmt.Errorf("%w: match %s %s -> %s", ErrInvalidTransition, match.MatchID, match.State, next)
	}
	match.State = next
	match.UpdatedAt = now
	if next == StateFailed {
		match.FailureReason = reason
	}
	s.recordEventLocked(Event{
		EntityType: "match",
		EntityID:   match.MatchID,
		MatchID:    match.MatchID,
		ServerID:   match.ServerID,
		State:      next,
		Reason:     reason,
		OccurredAt: now,
	})
	return nil
}

func (s *Service) setAssignmentStateLocked(assignment *Assignment, next LifecycleState, reason string, now time.Time) error {
	if assignment == nil {
		return ErrNotFound
	}
	if assignment.State == next {
		return nil
	}
	if !canTransition(assignment.State, next) {
		return fmt.Errorf("%w: assignment %s %s -> %s", ErrInvalidTransition, assignment.AssignmentID, assignment.State, next)
	}
	assignment.State = next
	assignment.UpdatedAt = now
	if next == StateFailed {
		assignment.FailureReason = reason
	}
	s.recordEventLocked(Event{
		EntityType: "assignment",
		EntityID:   assignment.AssignmentID,
		TicketID:   assignment.TicketID,
		MatchID:    assignment.MatchID,
		ServerID:   assignment.ServerID,
		PlayerID:   assignment.PlayerID,
		State:      next,
		Reason:     reason,
		OccurredAt: now,
	})
	return nil
}

func (s *Service) assignmentsForMatchLocked(match *Match) []Assignment {
	assignments := make([]Assignment, 0, len(match.AssignmentIDs))
	for _, assignmentID := range match.AssignmentIDs {
		if assignment := s.assignments[assignmentID]; assignment != nil {
			assignments = append(assignments, copyAssignment(assignment))
		}
	}
	return assignments
}

func (s *Service) compactQueueLocked(key string) {
	queue := s.queues[key]
	if len(queue) == 0 {
		return
	}
	compacted := queue[:0]
	for _, ticketID := range queue {
		ticket := s.tickets[ticketID]
		if ticket != nil && ticket.State == StateQueued {
			compacted = append(compacted, ticketID)
		}
	}
	if len(compacted) == 0 {
		delete(s.queues, key)
		return
	}
	s.queues[key] = compacted
}

func (s *Service) recordEventLocked(event Event) {
	s.nextEventSequence++
	event.Sequence = s.nextEventSequence
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.cfg.Clock()
	}
	s.events = append(s.events, event)
}

func canTransition(from LifecycleState, to LifecycleState) bool {
	switch from {
	case StateQueued:
		return to == StateMatched || to == StateCanceled || to == StateExpired || to == StateFailed
	case StateMatched:
		return to == StateAllocating || to == StateFailed
	case StateAllocating:
		return to == StateAssigned || to == StateFailed
	case StateAssigned:
		return to == StateConnecting || to == StateInGame || to == StateExpired || to == StateFailed || to == StateEnded
	case StateConnecting:
		return to == StateInGame || to == StateExpired || to == StateFailed || to == StateEnded
	case StateInGame:
		return to == StateEnded || to == StateFailed
	default:
		return false
	}
}

func isTerminal(state LifecycleState) bool {
	return state == StateEnded || state == StateFailed || state == StateCanceled || state == StateExpired
}

func poolKey(gameID string, buildID string) string {
	return gameID + "\x00" + buildID
}

func playerPoolKey(gameID string, buildID string, playerID string) string {
	return poolKey(gameID, buildID) + "\x00" + playerID
}

func idempotencyKey(req CreateTicketRequest) string {
	return playerPoolKey(req.GameID, req.BuildID, req.PlayerID) + "\x00" + req.IdempotencyKey
}

func copyTicket(ticket *Ticket) Ticket {
	if ticket == nil {
		return Ticket{}
	}
	return *ticket
}

func copyMatch(match *Match) Match {
	if match == nil {
		return Match{}
	}
	copied := *match
	copied.PlayerIDs = slices.Clone(match.PlayerIDs)
	copied.TicketIDs = slices.Clone(match.TicketIDs)
	copied.AssignmentIDs = slices.Clone(match.AssignmentIDs)
	return copied
}

func copyAssignment(assignment *Assignment) Assignment {
	if assignment == nil {
		return Assignment{}
	}
	return *assignment
}
