package room

import (
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/game"
)

type EventType string

const (
	EventTypeRoomCreated        EventType = "RoomCreated"
	EventTypePlayerJoined       EventType = "PlayerJoined"
	EventTypeGameMoveApplied    EventType = "GameMoveApplied"
	EventTypePlayerResigned     EventType = "PlayerResigned"
	EventTypeDrawOffered        EventType = "DrawOffered"
	EventTypeSecretSet          EventType = "SecretSet"
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
	GameType         game.Type
	CommandType      game.CommandType
	CommandPayload   any
	StateHash        uint64
	Status           game.Status
	ViewScope        game.ViewScope
	Reason           string
	OccurredAt       time.Time
}

func NewRoomCreatedEvent(state RoomState) RoomEvent {
	return RoomEvent{
		Type:       EventTypeRoomCreated,
		RoomID:     state.RoomID,
		Revision:   state.Revision,
		GameType:   state.GameType,
		OccurredAt: state.CreatedAt,
	}
}

func NewPlayerJoinedEvent(before RoomState, after RoomState, snapshot StateSnapshot, playerID string, at time.Time) RoomEvent {
	return RoomEvent{
		Type: EventTypePlayerJoined, RoomID: after.RoomID, PlayerID: playerID,
		Revision: after.Revision, PreviousRevision: before.Revision, NewRevision: after.Revision,
		GameType: after.GameType, StateHash: snapshot.Game.StateHash, Status: snapshot.Game.Status,
		OccurredAt: at,
	}
}

func NewGameCommandEvent(delta StateDelta) (RoomEvent, error) {
	eventType := EventType("")
	switch delta.Game.CommandType {
	case game.CommandDoMove:
		eventType = EventTypeGameMoveApplied
	case game.CommandResign:
		eventType = EventTypePlayerResigned
	case game.CommandOfferDraw:
		eventType = EventTypeDrawOffered
	case game.CommandSetSecret:
		eventType = EventTypeSecretSet
	default:
		return RoomEvent{}, fmt.Errorf("%w: %d", ErrInvalidCommand, delta.Game.CommandType)
	}

	return RoomEvent{
		Type:             eventType,
		RoomID:           delta.RoomID,
		PlayerID:         delta.ChangedByPlayerID,
		PreviousRevision: delta.PreviousRevision,
		NewRevision:      delta.NewRevision,
		GameType:         delta.Game.Type,
		CommandType:      delta.Game.CommandType,
		CommandPayload:   delta.Game.CommandPayload,
		StateHash:        delta.Game.StateHash,
		Status:           delta.Game.Status,
		OccurredAt:       delta.AppliedAt,
	}, nil
}

func NewSnapshotSentEvent(snapshot StateSnapshot, playerID string, viewScope game.ViewScope, at time.Time) RoomEvent {
	return RoomEvent{
		Type:       EventTypeSnapshotSent,
		RoomID:     snapshot.RoomID,
		PlayerID:   playerID,
		Revision:   snapshot.Revision,
		GameType:   snapshot.Game.Type,
		StateHash:  snapshot.Game.StateHash,
		Status:     snapshot.Game.Status,
		ViewScope:  viewScope,
		OccurredAt: at,
	}
}

func NewPlayerDisconnectedEvent(state RoomState, playerID string, reason string, at time.Time) RoomEvent {
	return RoomEvent{
		Type:       EventTypePlayerDisconnected,
		RoomID:     state.RoomID,
		PlayerID:   playerID,
		Revision:   state.Revision,
		GameType:   state.GameType,
		Reason:     reason,
		OccurredAt: at,
	}
}
