package room

import (
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/game"
)

func ReplayEvents(module game.Module, events []RoomEvent) (RoomState, error) {
	if module == nil {
		return RoomState{}, ErrNilGameModule
	}
	if len(events) == 0 {
		return RoomState{}, fmt.Errorf("replay events: no events")
	}

	var state RoomState
	var initialized bool
	var lastSequence uint64

	for _, event := range events {
		if event.Sequence == 0 {
			return state, fmt.Errorf("replay event missing sequence: type=%s room=%q", event.Type, event.RoomID)
		}
		if lastSequence != 0 && event.Sequence <= lastSequence {
			return state, fmt.Errorf("replay event sequence regression: previous=%d current=%d", lastSequence, event.Sequence)
		}
		lastSequence = event.Sequence

		if event.RoomID == "" {
			return state, ErrEmptyRoomID
		}

		if event.Type == EventTypeRoomCreated {
			if initialized {
				return state, fmt.Errorf("replay duplicate room created event: room=%q", event.RoomID)
			}
			if event.GameType != game.TypeUnspecified && event.GameType != module.Type() {
				return state, fmt.Errorf("replay game type mismatch: event=%d module=%d", event.GameType, module.Type())
			}
			gameState, err := module.NewState(event.OccurredAt)
			if err != nil {
				return state, fmt.Errorf("replay create game state: %w", err)
			}
			state = NewState(event.RoomID, module.Type(), gameState, event.OccurredAt)
			state.Revision = event.Revision
			state.UpdatedAt = event.OccurredAt
			initialized = true
			continue
		}

		if !initialized {
			return state, fmt.Errorf("replay event before room created: type=%s room=%q", event.Type, event.RoomID)
		}
		if event.RoomID != state.RoomID {
			return state, fmt.Errorf("replay room mismatch: event=%q state=%q", event.RoomID, state.RoomID)
		}

		switch event.Type {
		case EventTypeGameMoveApplied, EventTypePlayerResigned, EventTypeDrawOffered, EventTypeSecretSet:
			next, err := replayGameEvent(module, state, event)
			if err != nil {
				return state, err
			}
			state = next
		case EventTypePlayerJoined:
			gameState, changed, err := module.PlayerJoined(state.GameState, event.PlayerID)
			if err != nil {
				return state, fmt.Errorf("replay player joined: %w", err)
			}
			state.GameState = gameState
			if event.NewRevision != 0 {
				if !changed || event.PreviousRevision != state.Revision || event.NewRevision != event.PreviousRevision+1 {
					return state, fmt.Errorf("replay invalid player join revision: previous=%d new=%d current=%d", event.PreviousRevision, event.NewRevision, state.Revision)
				}
				state.Revision = event.NewRevision
			}
			state.UpdatedAt = nonZeroTime(event.OccurredAt, state.UpdatedAt)
			if event.StateHash != 0 {
				snapshot, snapshotErr := BuildSnapshot(module, state)
				if snapshotErr != nil || snapshot.Game.StateHash != event.StateHash {
					return state, fmt.Errorf("replay player join state hash mismatch")
				}
			}
		case EventTypeSnapshotSent, EventTypePlayerDisconnected:
			continue
		default:
			return state, fmt.Errorf("replay unknown event type: %s", event.Type)
		}
	}

	return state, nil
}

func replayGameEvent(module game.Module, state RoomState, event RoomEvent) (RoomState, error) {
	if event.PreviousRevision != state.Revision {
		return state, fmt.Errorf("replay revision gap: expected previous=%d got=%d", state.Revision, event.PreviousRevision)
	}
	if event.NewRevision != event.PreviousRevision+1 {
		return state, fmt.Errorf("replay invalid new revision: previous=%d new=%d", event.PreviousRevision, event.NewRevision)
	}

	next, _, err := ApplyGameCommand(module, state, GameCommand{
		RoomID:           event.RoomID,
		PlayerID:         event.PlayerID,
		Type:             event.CommandType,
		Payload:          event.CommandPayload,
		ExpectedRevision: event.PreviousRevision,
		ReceivedAt:       event.OccurredAt,
	}, event.OccurredAt)
	if err != nil {
		return state, fmt.Errorf("replay game event: %w", err)
	}

	if event.StateHash != 0 {
		snapshot, err := BuildSnapshot(module, next)
		if err != nil {
			return state, fmt.Errorf("replay snapshot: %w", err)
		}
		if snapshot.Game.StateHash != event.StateHash {
			return state, fmt.Errorf("replay state hash mismatch: got=%d want=%d", snapshot.Game.StateHash, event.StateHash)
		}
	}
	return next, nil
}

func ReplayDeltas(module game.Module, initial RoomState, deltas []StateDelta) (RoomState, error) {
	state := initial
	for _, delta := range deltas {
		if delta.RoomID != state.RoomID {
			return state, fmt.Errorf("replay room mismatch: delta=%q state=%q", delta.RoomID, state.RoomID)
		}
		if delta.PreviousRevision != state.Revision {
			return state, fmt.Errorf("replay revision gap: expected previous=%d got=%d", state.Revision, delta.PreviousRevision)
		}

		next, _, err := ApplyGameCommand(module, state, GameCommand{
			RoomID:           delta.RoomID,
			PlayerID:         delta.ChangedByPlayerID,
			Type:             delta.Game.CommandType,
			Payload:          delta.Game.CommandPayload,
			ExpectedRevision: delta.PreviousRevision,
			ReceivedAt:       delta.AppliedAt,
		}, delta.AppliedAt)
		if err != nil {
			return state, err
		}
		state = next
	}
	return state, nil
}

func nonZeroTime(candidate time.Time, fallback time.Time) time.Time {
	if candidate.IsZero() {
		return fallback
	}
	return candidate
}
