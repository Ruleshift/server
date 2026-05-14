package room

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type EventStore interface {
	Append(ctx context.Context, event RoomEvent) (RoomEvent, error)
	List(ctx context.Context, roomID string) ([]RoomEvent, error)
}

type InMemoryEventStore struct {
	mu           sync.Mutex
	nextSequence uint64
	events       []RoomEvent
}

func NewInMemoryEventStore() *InMemoryEventStore {
	return &InMemoryEventStore{events: make([]RoomEvent, 0)}
}

func (s *InMemoryEventStore) Append(ctx context.Context, event RoomEvent) (RoomEvent, error) {
	if err := ctx.Err(); err != nil {
		return RoomEvent{}, fmt.Errorf("append room event: %w", err)
	}
	if event.Type == "" {
		return RoomEvent{}, fmt.Errorf("room event type must not be empty")
	}
	if event.RoomID == "" {
		return RoomEvent{}, ErrEmptyRoomID
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextSequence++
	event.Sequence = s.nextSequence
	s.events = append(s.events, event)
	return event, nil
}

func (s *InMemoryEventStore) List(ctx context.Context, roomID string) ([]RoomEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list room events: %w", err)
	}
	if roomID == "" {
		return nil, ErrEmptyRoomID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	events := make([]RoomEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.RoomID == roomID {
			events = append(events, event)
		}
	}
	return events, nil
}
