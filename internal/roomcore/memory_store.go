package roomcore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/module"
)

type memoryRoom struct {
	route     Route
	state     State
	snapshots []Snapshot
	events    []Event
}

type MemoryStore struct {
	mu    sync.Mutex
	rooms map[string]*memoryRoom
	next  uint64
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{rooms: make(map[string]*memoryRoom)} }

func (s *MemoryStore) Create(ctx context.Context, state State, event Event, snapshot Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rooms[state.Route.RoomID]; exists {
		return ErrRoomExists
	}
	if state.Route.InviteCode != "" {
		for _, room := range s.rooms {
			if room.route.InviteCode == state.Route.InviteCode {
				return ErrInviteCodeExists
			}
		}
	}
	s.next++
	event.Sequence = s.next
	s.rooms[state.Route.RoomID] = &memoryRoom{route: state.Route, state: cloneState(state), snapshots: []Snapshot{cloneSnapshot(snapshot)}, events: []Event{cloneEvent(event)}}
	return nil
}

func (s *MemoryStore) Load(ctx context.Context, roomID string) (Route, *Snapshot, []Event, error) {
	if err := ctx.Err(); err != nil {
		return Route{}, nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[roomID]
	if room == nil {
		return Route{}, nil, nil, ErrRoomNotFound
	}
	var snapshot *Snapshot
	if len(room.snapshots) > 0 {
		copy := cloneSnapshot(room.snapshots[len(room.snapshots)-1])
		snapshot = &copy
	}
	events := make([]Event, 0, len(room.events))
	for _, event := range room.events {
		if snapshot == nil || event.NewRevision > snapshot.Revision {
			events = append(events, cloneEvent(event))
		}
	}
	return room.route, snapshot, events, nil
}

func (s *MemoryStore) Commit(ctx context.Context, state State, event Event, snapshot *Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[state.Route.RoomID]
	if room == nil {
		return ErrRoomNotFound
	}
	if room.state.Revision != event.PreviousRevision {
		return fmt.Errorf("%w: stored=%d event=%d", ErrRevisionMismatch, room.state.Revision, event.PreviousRevision)
	}
	s.next++
	event.Sequence = s.next
	room.state = cloneState(state)
	room.events = append(room.events, cloneEvent(event))
	if snapshot != nil {
		room.snapshots = append(room.snapshots, cloneSnapshot(*snapshot))
	}
	return nil
}

func (s *MemoryStore) SaveSnapshot(ctx context.Context, snapshot Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[snapshot.RoomID]
	if room == nil {
		return ErrRoomNotFound
	}
	if len(room.snapshots) > 0 && room.snapshots[len(room.snapshots)-1].Revision == snapshot.Revision {
		room.snapshots[len(room.snapshots)-1] = cloneSnapshot(snapshot)
	} else {
		room.snapshots = append(room.snapshots, cloneSnapshot(snapshot))
	}
	return nil
}

func (s *MemoryStore) Route(ctx context.Context, roomID string) (Route, error) {
	if err := ctx.Err(); err != nil {
		return Route{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[roomID]
	if room == nil {
		return Route{}, ErrRoomNotFound
	}
	return room.route, nil
}

func (s *MemoryStore) RouteByInviteCode(ctx context.Context, inviteCode string) (Route, error) {
	if err := ctx.Err(); err != nil {
		return Route{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, room := range s.rooms {
		if room.route.InviteCode == inviteCode && room.route.InviteDeadline.After(now) {
			return room.route, nil
		}
	}
	return Route{}, ErrInviteCodeNotFound
}

func cloneOpaque(value module.OpaqueState) module.OpaqueState {
	value.Payload = append([]byte(nil), value.Payload...)
	return value
}
func cloneState(value State) State          { value.Opaque = cloneOpaque(value.Opaque); return value }
func cloneSnapshot(value Snapshot) Snapshot { value.State = cloneOpaque(value.State); return value }
func cloneEvent(value Event) Event {
	value.Input = cloneOpaque(value.Input)
	value.Delta = cloneOpaque(value.Delta)
	return value
}
