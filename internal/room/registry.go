package room

import (
	"context"
	"fmt"
	"sync"
	"time"
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

	var runtime *RoomRuntime
	var err error
	if r.cfg.EventStore != nil {
		timeout := r.cfg.EventStoreTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		events, listErr := r.cfg.EventStore.List(ctx, roomID)
		cancel()
		if listErr != nil {
			return nil, false, fmt.Errorf("load room events: %w", listErr)
		}
		if len(events) > 0 {
			state, replayErr := ReplayEvents(r.cfg.GameModule, events)
			if replayErr != nil {
				return nil, false, fmt.Errorf("replay room events: %w", replayErr)
			}
			runtime, err = NewRuntimeFromState(state, r.cfg)
		} else {
			runtime, err = NewRuntime(roomID, r.cfg)
		}
	} else {
		runtime, err = NewRuntime(roomID, r.cfg)
	}
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
