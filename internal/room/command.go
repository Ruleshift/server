package room

import (
	"errors"
	"fmt"
	"time"
)

type Operation uint8

const (
	OperationUnspecified Operation = iota
	OperationAdd
	OperationSet
)

var (
	ErrInvalidOperation = errors.New("invalid room operation")
	ErrRevisionMismatch = errors.New("expected revision does not match room revision")
	ErrRoomIDMismatch   = errors.New("command room id does not match runtime room id")
	ErrEmptyPlayerID    = errors.New("player id must not be empty")
	ErrEmptyRoomID      = errors.New("room id must not be empty")
)

type IntCommand struct {
	RoomID           string
	PlayerID         string
	Operation        Operation
	Value            int64
	ExpectedRevision uint64
	ReceivedAt       time.Time
}

type StateDelta struct {
	RoomID            string
	PreviousValue     int64
	NewValue          int64
	PreviousRevision  uint64
	NewRevision       uint64
	ChangedByPlayerID string
	Operation         Operation
	Operand           int64
	AppliedAt         time.Time
}

func ApplyIntCommand(state RoomState, cmd IntCommand, now time.Time) (RoomState, StateDelta, error) {
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

	previousValue := state.Value
	previousRevision := state.Revision

	switch cmd.Operation {
	case OperationAdd:
		state.Value += cmd.Value
	case OperationSet:
		state.Value = cmd.Value
	default:
		return state, StateDelta{}, fmt.Errorf("%w: %d", ErrInvalidOperation, cmd.Operation)
	}

	state.Revision++
	state.UpdatedAt = now

	previous := RoomState{RoomID: state.RoomID, Value: previousValue, Revision: previousRevision}
	return state, BuildDelta(previous, state, cmd, now), nil
}

func BuildDelta(previous RoomState, next RoomState, cmd IntCommand, appliedAt time.Time) StateDelta {
	return StateDelta{
		RoomID:            next.RoomID,
		PreviousValue:     previous.Value,
		NewValue:          next.Value,
		PreviousRevision:  previous.Revision,
		NewRevision:       next.Revision,
		ChangedByPlayerID: cmd.PlayerID,
		Operation:         cmd.Operation,
		Operand:           cmd.Value,
		AppliedAt:         appliedAt,
	}
}
