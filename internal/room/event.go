package room

import (
	"fmt"
	"time"
)

type EventType string

const (
	EventTypeRoomCreated        EventType = "RoomCreated"
	EventTypePlayerJoined       EventType = "PlayerJoined"
	EventTypeIntAdded           EventType = "IntAdded"
	EventTypeIntSet             EventType = "IntSet"
	EventTypeSnapshotSent       EventType = "SnapshotSent"
	EventTypePlayerDisconnected EventType = "PlayerDisconnected"
)

type RoomEvent struct {
	Sequence         uint64
	Type             EventType
	RoomID           string
	PlayerID         string
	Revision         uint64
	PreviousRevision uint64
	NewRevision      uint64
	PreviousValue    int64
	NewValue         int64
	Operand          int64
	Reason           string
	OccurredAt       time.Time
}

func NewRoomCreatedEvent(state RoomState) RoomEvent {
	return RoomEvent{
		Type:       EventTypeRoomCreated,
		RoomID:     state.RoomID,
		Revision:   state.Revision,
		NewValue:   state.Value,
		OccurredAt: state.CreatedAt,
	}
}

func NewPlayerJoinedEvent(state RoomState, playerID string, at time.Time) RoomEvent {
	return RoomEvent{
		Type:       EventTypePlayerJoined,
		RoomID:     state.RoomID,
		PlayerID:   playerID,
		Revision:   state.Revision,
		NewValue:   state.Value,
		OccurredAt: at,
	}
}

func NewIntEvent(delta StateDelta) (RoomEvent, error) {
	eventType := EventType("")
	switch delta.Operation {
	case OperationAdd:
		eventType = EventTypeIntAdded
	case OperationSet:
		eventType = EventTypeIntSet
	default:
		return RoomEvent{}, fmt.Errorf("%w: %d", ErrInvalidOperation, delta.Operation)
	}

	return RoomEvent{
		Type:             eventType,
		RoomID:           delta.RoomID,
		PlayerID:         delta.ChangedByPlayerID,
		PreviousRevision: delta.PreviousRevision,
		NewRevision:      delta.NewRevision,
		PreviousValue:    delta.PreviousValue,
		NewValue:         delta.NewValue,
		Operand:          delta.Operand,
		OccurredAt:       delta.AppliedAt,
	}, nil
}

func NewSnapshotSentEvent(snapshot StateSnapshot, playerID string, at time.Time) RoomEvent {
	return RoomEvent{
		Type:       EventTypeSnapshotSent,
		RoomID:     snapshot.RoomID,
		PlayerID:   playerID,
		Revision:   snapshot.Revision,
		NewValue:   snapshot.Value,
		OccurredAt: at,
	}
}

func NewPlayerDisconnectedEvent(state RoomState, playerID string, reason string, at time.Time) RoomEvent {
	return RoomEvent{
		Type:       EventTypePlayerDisconnected,
		RoomID:     state.RoomID,
		PlayerID:   playerID,
		Revision:   state.Revision,
		NewValue:   state.Value,
		Reason:     reason,
		OccurredAt: at,
	}
}
