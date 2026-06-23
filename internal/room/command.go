package room

import (
	"errors"
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/game"
)

var (
	ErrInvalidCommand   = errors.New("invalid room command")
	ErrRevisionMismatch = errors.New("expected revision does not match room revision")
	ErrRoomIDMismatch   = errors.New("command room id does not match runtime room id")
	ErrEmptyPlayerID    = errors.New("player id must not be empty")
	ErrEmptyRoomID      = errors.New("room id must not be empty")
	ErrNilGameModule    = errors.New("game module must not be nil")
)

type GameCommand struct {
	RoomID           string
	PlayerID         string
	Type             game.CommandType
	Payload          any
	ExpectedRevision uint64
	ReceivedAt       time.Time
}

type StateDelta struct {
	RoomID            string
	PreviousRevision  uint64
	NewRevision       uint64
	ChangedByPlayerID string
	Game              game.Delta
	AppliedAt         time.Time
}

type ProjectedStateDelta struct {
	RoomID            string
	PreviousRevision  uint64
	NewRevision       uint64
	ChangedByPlayerID string
	Game              game.ViewDelta
}

func ProjectDelta(module game.Module, before RoomState, after RoomState, delta StateDelta, viewer game.Viewer) (ProjectedStateDelta, error) {
	projected, err := module.ProjectDelta(before.GameState, after.GameState, delta.Game, viewer)
	if err != nil {
		return ProjectedStateDelta{}, err
	}
	return ProjectedStateDelta{
		RoomID: delta.RoomID, PreviousRevision: delta.PreviousRevision, NewRevision: delta.NewRevision,
		ChangedByPlayerID: delta.ChangedByPlayerID, Game: projected,
	}, nil
}

func ApplyGameCommand(module game.Module, state RoomState, cmd GameCommand, now time.Time) (RoomState, StateDelta, error) {
	if module == nil {
		return state, StateDelta{}, ErrNilGameModule
	}
	if cmd.PlayerID == "" {
		return state, StateDelta{}, ErrEmptyPlayerID
	}
	if cmd.RoomID == "" {
		return state, StateDelta{}, ErrEmptyRoomID
	}
	if cmd.RoomID != state.RoomID {
		return state, StateDelta{}, fmt.Errorf("%w: command=%q state=%q", ErrRoomIDMismatch, cmd.RoomID, state.RoomID)
	}
	if cmd.ExpectedRevision != 0 && cmd.ExpectedRevision != state.Revision {
		return state, StateDelta{}, fmt.Errorf("%w: expected=%d actual=%d", ErrRevisionMismatch, cmd.ExpectedRevision, state.Revision)
	}

	previousRevision := state.Revision
	nextGameState, gameDelta, err := module.Apply(state.GameState, game.Command{
		PlayerID: cmd.PlayerID,
		Type:     cmd.Type,
		Payload:  cmd.Payload,
		At:       now,
	})
	if err != nil {
		return state, StateDelta{}, err
	}
	if gameDelta.Type == game.TypeUnspecified {
		return state, StateDelta{}, ErrInvalidCommand
	}

	state.GameState = nextGameState
	state.GameType = module.Type()
	state.Revision++
	state.UpdatedAt = now

	return state, BuildDelta(state.RoomID, previousRevision, state.Revision, cmd.PlayerID, gameDelta, now), nil
}

func BuildDelta(roomID string, previousRevision uint64, newRevision uint64, playerID string, gameDelta game.Delta, appliedAt time.Time) StateDelta {
	return StateDelta{
		RoomID:            roomID,
		PreviousRevision:  previousRevision,
		NewRevision:       newRevision,
		ChangedByPlayerID: playerID,
		Game:              gameDelta,
		AppliedAt:         appliedAt,
	}
}
