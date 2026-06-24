package roomcore

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Ruleshift/server/internal/module"
)

type Registry struct {
	mu        sync.Mutex
	store     Store
	resolver  module.Resolver
	queueSize int
	ctx       context.Context
	cancel    context.CancelFunc
	rooms     map[string]*Runtime
}

func NewRegistry(store Store, resolver module.Resolver, queueSize int) (*Registry, error) {
	if store == nil || resolver == nil {
		return nil, fmt.Errorf("room store and module resolver are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{store: store, resolver: resolver, queueSize: queueSize, ctx: ctx, cancel: cancel, rooms: make(map[string]*Runtime)}, nil
}

func (r *Registry) Create(ctx context.Context, route Route) (*Runtime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.rooms[route.RoomID]; exists {
		return nil, ErrRoomExists
	}
	if _, err := r.store.Route(ctx, route.RoomID); err == nil {
		return nil, ErrRoomExists
	} else if !errors.Is(err, ErrRoomNotFound) {
		return nil, err
	}
	runtime, err := r.resolver.Resolve(ctx, route.Module)
	if err != nil {
		return nil, err
	}
	room, err := Create(ctx, route, RuntimeConfig{QueueSize: r.queueSize, Store: r.store, Module: runtime})
	if err != nil {
		return nil, err
	}
	r.rooms[route.RoomID] = room
	go room.Run(r.ctx)
	return room, nil
}

func (r *Registry) Get(ctx context.Context, roomID string) (*Runtime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if room := r.rooms[roomID]; room != nil {
		return room, nil
	}
	route, err := r.store.Route(ctx, roomID)
	if err != nil {
		return nil, err
	}
	runtime, err := r.resolver.Resolve(ctx, route.Module)
	if err != nil {
		return nil, err
	}
	room, err := Restore(ctx, route, RuntimeConfig{QueueSize: r.queueSize, Store: r.store, Module: runtime})
	if err != nil {
		return nil, err
	}
	r.rooms[roomID] = room
	go room.Run(r.ctx)
	return room, nil
}

func (r *Registry) Close()         { r.cancel() }
func (r *Registry) RoomCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.rooms) }
