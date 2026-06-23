package game

import (
	"context"
	"errors"
	"time"
)

type Type uint8

const (
	TypeUnspecified Type = iota
	TypeXiangqi
	TypeHiddenNumber
)

type CommandType uint8

const (
	CommandUnspecified CommandType = iota
	CommandDoMove
	CommandResign
	CommandOfferDraw
	CommandSetSecret
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
	ErrRoomFull         = errors.New("game seats are full")
)

type JoinMode uint8

const (
	JoinModeUnspecified JoinMode = iota
	JoinModePlayer
	JoinModeSpectator
)

type ViewScope uint8

const (
	ViewScopeUnspecified ViewScope = iota
	ViewScopePlayer
	ViewScopePublic
	ViewScopeFull
)

type Viewer struct {
	PlayerID string
	JoinMode JoinMode
	Scope    ViewScope
}

func (v Viewer) CanSeePrivateOf(playerID string) bool {
	return v.CanSeeFullState() || (v.Scope == ViewScopePlayer && v.PlayerID != "" && v.PlayerID == playerID)
}

func (v Viewer) CanSeeFullState() bool {
	return v.JoinMode == JoinModeSpectator && v.Scope == ViewScopeFull
}

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

type ViewSnapshot struct {
	Type     Type
	Status   Status
	ViewHash uint64
	Payload  any
}

type ViewDelta struct {
	Type            Type
	CommandType     CommandType
	Status          Status
	ViewHash        uint64
	Payload         any
	NoVisibleChange bool
}

type Module interface {
	Type() Type
	NewState(now time.Time) (any, error)
	// PlayerJoined and Apply must not mutate state in place. The room runtime
	// publishes returned state only after its durable event append succeeds.
	PlayerJoined(state any, playerID string) (next any, changed bool, err error)
	Snapshot(state any) (Snapshot, error)
	ProjectSnapshot(state any, viewer Viewer) (ViewSnapshot, error)
	ProjectDelta(before any, after any, delta Delta, viewer Viewer) (ViewDelta, error)
	Apply(state any, command Command) (any, Delta, error)
}

// DatabaseMigration is one immutable, forward-only module database migration.
// Versions are scoped to DatabaseDefinition.Name and must be strictly positive.
type DatabaseMigration struct {
	Version uint64
	Name    string
	SQL     string
}

// DatabaseDefinition lets a game module own its durable schema without coupling
// the module to a concrete database driver. The platform creates one PostgreSQL
// database per definition and applies these migrations after its base room schema.
type DatabaseDefinition struct {
	Name       string
	Migrations []DatabaseMigration
}

// DatabaseModule is optional. Modules without it remain usable with the in-memory
// event store, which keeps small tests and development modules lightweight.
type DatabaseModule interface {
	Module
	DatabaseDefinition() DatabaseDefinition
}

// CommandPayloadCodec preserves concrete module command payloads in the generic
// room event log so replay after a restart receives the same Go payload type.
type CommandPayloadCodec interface {
	MarshalCommandPayload(ctx context.Context, commandType CommandType, payload any) ([]byte, error)
	UnmarshalCommandPayload(ctx context.Context, commandType CommandType, payload []byte) (any, error)
}
