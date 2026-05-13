package room

import (
	"fmt"
	"sync"
)

type Registry struct {
	mu    sync.Mutex
	cfg   RuntimeConfig
	rooms map[string]*RoomRuntime
}

func NewRegistry(cfg RuntimeConfig) *Registry {
	return &Registry{
		cfg:   cfg,
		rooms: make(map[string]*RoomRuntime),
	}
}

func (r *Registry) GetOrCreate(roomID string) (*RoomRuntime, bool, error) {
	if roomID == "" {
		return nil, false, fmt.Errorf("room id must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if runtime, ok := r.rooms[roomID]; ok {
		return runtime, false, nil
	}

	runtime, err := NewRuntime(roomID, r.cfg)
	if err != nil {
		return nil, false, fmt.Errorf("create room runtime: %w", err)
	}

	r.rooms[roomID] = runtime
	return runtime, true, nil
}

func (r *Registry) RoomCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rooms)
}
