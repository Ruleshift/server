package allocator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

type ServerStatus string

const (
	ServerStatusAvailable   ServerStatus = "available"
	ServerStatusUnavailable ServerStatus = "unavailable"
)

var (
	ErrServerNotFound       = errors.New("server not found")
	ErrNoServerCapacity     = errors.New("no server capacity available")
	ErrReservationNotFound  = errors.New("reservation not found")
	ErrInvalidAllocatorArgs = errors.New("invalid allocator arguments")
)

type Config struct {
	ReservationTTL time.Duration
	Clock          func() time.Time
}

type Registry struct {
	mu                 sync.Mutex
	reservationTTL     time.Duration
	clock              func() time.Time
	servers            map[string]*Server
	serversByPool      map[string]map[string]struct{}
	reservations       map[string]*Reservation
	reservationByMatch map[string]string
}

type Server struct {
	ServerID      string
	GameID        string
	BuildID       string
	Endpoint      string
	CapacitySeats int
	ReservedSeats int
	Status        ServerStatus
	UpdatedAt     time.Time
}

type RegisterServerRequest struct {
	ServerID      string
	GameID        string
	BuildID       string
	Endpoint      string
	CapacitySeats int
	Status        ServerStatus
}

type ReservationRequest struct {
	GameID  string
	BuildID string
	MatchID string
	Seats   int
}

type Reservation struct {
	ReservationID string
	ServerID      string
	GameID        string
	BuildID       string
	Endpoint      string
	MatchID       string
	Seats         int
	ExpiresAt     time.Time
}

func NewRegistry(cfg Config) (*Registry, error) {
	if cfg.ReservationTTL <= 0 {
		return nil, fmt.Errorf("reservation TTL must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time {
			return time.Now().UTC()
		}
	}

	return &Registry{
		reservationTTL:     cfg.ReservationTTL,
		clock:              cfg.Clock,
		servers:            make(map[string]*Server),
		serversByPool:      make(map[string]map[string]struct{}),
		reservations:       make(map[string]*Reservation),
		reservationByMatch: make(map[string]string),
	}, nil
}

func (r *Registry) RegisterServer(ctx context.Context, req RegisterServerRequest) (Server, error) {
	if err := ctx.Err(); err != nil {
		return Server{}, fmt.Errorf("register server: %w", err)
	}
	if req.ServerID == "" || req.GameID == "" || req.BuildID == "" || req.Endpoint == "" || req.CapacitySeats <= 0 {
		return Server{}, ErrInvalidAllocatorArgs
	}
	if req.Status == "" {
		req.Status = ServerStatusAvailable
	}
	if req.Status != ServerStatusAvailable && req.Status != ServerStatusUnavailable {
		return Server{}, fmt.Errorf("%w: unknown server status %q", ErrInvalidAllocatorArgs, req.Status)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock()
	if previous := r.servers[req.ServerID]; previous != nil {
		r.removeServerFromPoolLocked(previous)
	}

	server := &Server{
		ServerID:      req.ServerID,
		GameID:        req.GameID,
		BuildID:       req.BuildID,
		Endpoint:      req.Endpoint,
		CapacitySeats: req.CapacitySeats,
		Status:        req.Status,
		UpdatedAt:     now,
	}
	if previous := r.servers[req.ServerID]; previous != nil {
		server.ReservedSeats = previous.ReservedSeats
	}
	r.servers[req.ServerID] = server
	r.addServerToPoolLocked(server)
	return copyServer(server), nil
}

func (r *Registry) Reserve(ctx context.Context, req ReservationRequest) (Reservation, error) {
	if err := ctx.Err(); err != nil {
		return Reservation{}, fmt.Errorf("reserve server: %w", err)
	}
	if req.GameID == "" || req.BuildID == "" || req.MatchID == "" || req.Seats <= 0 {
		return Reservation{}, ErrInvalidAllocatorArgs
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock()
	r.cleanupExpiredLocked(now)

	if reservationID := r.reservationByMatch[req.MatchID]; reservationID != "" {
		if reservation := r.reservations[reservationID]; reservation != nil && reservation.ExpiresAt.After(now) {
			return copyReservation(reservation), nil
		}
	}

	pool := r.serversByPool[poolKey(req.GameID, req.BuildID)]
	for serverID := range pool {
		server := r.servers[serverID]
		if server == nil || server.Status != ServerStatusAvailable {
			continue
		}
		if server.CapacitySeats-server.ReservedSeats < req.Seats {
			continue
		}

		reservationID, err := randomID("reservation")
		if err != nil {
			return Reservation{}, err
		}
		reservation := &Reservation{
			ReservationID: reservationID,
			ServerID:      server.ServerID,
			GameID:        server.GameID,
			BuildID:       server.BuildID,
			Endpoint:      server.Endpoint,
			MatchID:       req.MatchID,
			Seats:         req.Seats,
			ExpiresAt:     now.Add(r.reservationTTL),
		}
		server.ReservedSeats += req.Seats
		server.UpdatedAt = now
		r.reservations[reservation.ReservationID] = reservation
		r.reservationByMatch[req.MatchID] = reservation.ReservationID
		return copyReservation(reservation), nil
	}

	return Reservation{}, ErrNoServerCapacity
}

func (r *Registry) Release(ctx context.Context, reservationID string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("release reservation: %w", err)
	}
	if reservationID == "" {
		return ErrInvalidAllocatorArgs
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.releaseLocked(reservationID, r.clock()) {
		return ErrReservationNotFound
	}
	return nil
}

func (r *Registry) CleanupExpired(ctx context.Context, now time.Time) int {
	if err := ctx.Err(); err != nil {
		return 0
	}
	if now.IsZero() {
		now = r.clock()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cleanupExpiredLocked(now)
}

func (r *Registry) SetStatus(ctx context.Context, serverID string, status ServerStatus) ([]Reservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("set server status: %w", err)
	}
	if serverID == "" {
		return nil, ErrInvalidAllocatorArgs
	}
	if status != ServerStatusAvailable && status != ServerStatusUnavailable {
		return nil, fmt.Errorf("%w: unknown server status %q", ErrInvalidAllocatorArgs, status)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	server := r.servers[serverID]
	if server == nil {
		return nil, ErrServerNotFound
	}
	server.Status = status
	server.UpdatedAt = r.clock()

	if status == ServerStatusAvailable {
		return nil, nil
	}

	released := make([]Reservation, 0)
	for reservationID, reservation := range r.reservations {
		if reservation.ServerID != serverID {
			continue
		}
		released = append(released, copyReservation(reservation))
		r.releaseLocked(reservationID, server.UpdatedAt)
	}
	return released, nil
}

func (r *Registry) ServerSnapshot(ctx context.Context, serverID string) (Server, error) {
	if err := ctx.Err(); err != nil {
		return Server{}, fmt.Errorf("server snapshot: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	server := r.servers[serverID]
	if server == nil {
		return Server{}, ErrServerNotFound
	}
	return copyServer(server), nil
}

func (r *Registry) cleanupExpiredLocked(now time.Time) int {
	expired := 0
	for reservationID, reservation := range r.reservations {
		if reservation.ExpiresAt.After(now) {
			continue
		}
		if r.releaseLocked(reservationID, now) {
			expired++
		}
	}
	return expired
}

func (r *Registry) releaseLocked(reservationID string, now time.Time) bool {
	reservation := r.reservations[reservationID]
	if reservation == nil {
		return false
	}
	if server := r.servers[reservation.ServerID]; server != nil {
		server.ReservedSeats -= reservation.Seats
		if server.ReservedSeats < 0 {
			server.ReservedSeats = 0
		}
		server.UpdatedAt = now
	}
	delete(r.reservations, reservationID)
	delete(r.reservationByMatch, reservation.MatchID)
	return true
}

func (r *Registry) addServerToPoolLocked(server *Server) {
	key := poolKey(server.GameID, server.BuildID)
	pool := r.serversByPool[key]
	if pool == nil {
		pool = make(map[string]struct{})
		r.serversByPool[key] = pool
	}
	pool[server.ServerID] = struct{}{}
}

func (r *Registry) removeServerFromPoolLocked(server *Server) {
	key := poolKey(server.GameID, server.BuildID)
	pool := r.serversByPool[key]
	if pool == nil {
		return
	}
	delete(pool, server.ServerID)
	if len(pool) == 0 {
		delete(r.serversByPool, key)
	}
}

func poolKey(gameID string, buildID string) string {
	return gameID + "\x00" + buildID
}

func copyServer(server *Server) Server {
	if server == nil {
		return Server{}
	}
	return *server
}

func copyReservation(reservation *Reservation) Reservation {
	if reservation == nil {
		return Reservation{}
	}
	return *reservation
}

func randomID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}
