package room

import (
	"errors"
	"testing"
	"time"
)

func TestApplyIntCommandAdd(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	state := NewState("room-1", now)

	next, delta, err := ApplyIntCommand(state, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationAdd,
		Value:     7,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApplyIntCommand returned error: %v", err)
	}

	if next.Value != 7 {
		t.Fatalf("Value = %d, want 7", next.Value)
	}
	if next.Revision != 1 {
		t.Fatalf("Revision = %d, want 1", next.Revision)
	}
	if delta.PreviousValue != 0 || delta.NewValue != 7 {
		t.Fatalf("delta values = %d -> %d, want 0 -> 7", delta.PreviousValue, delta.NewValue)
	}
}

func TestApplyIntCommandSet(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	state := NewState("room-1", now)
	state.Value = 5
	state.Revision = 3

	next, _, err := ApplyIntCommand(state, IntCommand{
		RoomID:           "room-1",
		PlayerID:         "player-1",
		Operation:        OperationSet,
		Value:            42,
		ExpectedRevision: 3,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApplyIntCommand returned error: %v", err)
	}

	if next.Value != 42 {
		t.Fatalf("Value = %d, want 42", next.Value)
	}
	if next.Revision != 4 {
		t.Fatalf("Revision = %d, want 4", next.Revision)
	}
}

func TestApplyIntCommandRejectsInvalidOperation(t *testing.T) {
	state := NewState("room-1", time.Unix(100, 0).UTC())

	_, _, err := ApplyIntCommand(state, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationUnspecified,
	}, time.Unix(101, 0).UTC())
	if !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("error = %v, want ErrInvalidOperation", err)
	}
}

func TestApplyIntCommandRejectsRevisionMismatch(t *testing.T) {
	state := NewState("room-1", time.Unix(100, 0).UTC())
	state.Revision = 9

	_, _, err := ApplyIntCommand(state, IntCommand{
		RoomID:           "room-1",
		PlayerID:         "player-1",
		Operation:        OperationAdd,
		Value:            1,
		ExpectedRevision: 8,
	}, time.Unix(101, 0).UTC())
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("error = %v, want ErrRevisionMismatch", err)
	}
}
