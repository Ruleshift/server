package room

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryEventStoreAssignsSequencesAndFiltersByRoom(t *testing.T) {
	store := NewInMemoryEventStore()
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()

	first, err := store.Append(ctx, RoomEvent{
		Type:       EventTypeRoomCreated,
		RoomID:     "room-1",
		OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}
	second, err := store.Append(ctx, RoomEvent{
		Type:       EventTypeRoomCreated,
		RoomID:     "room-2",
		OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("second Append returned error: %v", err)
	}

	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d/%d, want 1/2", first.Sequence, second.Sequence)
	}

	events, err := store.List(ctx, "room-1")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("room-1 event count = %d, want 1", len(events))
	}
	if events[0].Sequence != 1 || events[0].RoomID != "room-1" {
		t.Fatalf("room-1 event = %#v, want sequence 1 room-1", events[0])
	}
}
