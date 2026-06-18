package game

import (
	"errors"
	"time"
)

type Type uint8

const (
	TypeUnspecified Type = iota
	TypeXiangqi
)

type CommandType uint8

const (
	CommandUnspecified CommandType = iota
	CommandDoMove
	CommandResign
	CommandOfferDraw
)

type Status uint8

const (
	StatusUnspecified Status = iota
	StatusActive
	StatusResigned
	StatusDrawOffered
	StatusDrawn
)

var (
	ErrInvalidCommand   = errors.New("invalid game command")
	ErrIllegalMove      = errors.New("illegal move")
	ErrGameFinished     = errors.New("game is finished")
	ErrPlayerNotSeated  = errors.New("player is not seated")
	ErrNotPlayersTurn   = errors.New("not player's turn")
	ErrUnsupportedState = errors.New("unsupported game state")
)

type Command struct {
	PlayerID string
	Type     CommandType
	Payload  any
	At       time.Time
}

type Snapshot struct {
	Type      Type
	Status    Status
	StateHash uint64
	Payload   any
}

type Delta struct {
	Type           Type
	CommandType    CommandType
	Status         Status
	StateHash      uint64
	CommandPayload any
	Payload        any
}

type Module interface {
	Type() Type
	NewState(now time.Time) (any, error)
	PlayerJoined(state any, playerID string) (any, error)
	Snapshot(state any) (Snapshot, error)
	Apply(state any, command Command) (any, Delta, error)
}
