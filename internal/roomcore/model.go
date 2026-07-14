// Package roomcore owns the generic authoritative room runtime. Module state is
// opaque protobuf bytes; this package must never import a game implementation.
package roomcore

import (
	"context"
	"errors"
	"time"

	"github.com/Ruleshift/server/internal/module"
)

var (
	ErrRoomNotFound     = errors.New("room not found")
	ErrRoomExists       = errors.New("room already exists")
	ErrInviteCodeExists = errors.New("room invite code already exists")
	ErrRevisionMismatch = errors.New("expected revision does not match room revision")
	ErrRuntimeClosed    = errors.New("room runtime is closed")
	ErrQueueFull        = errors.New("room input queue is full")
)

const (
	InviteCodeLength = 6
	InviteCodeTTL    = 24 * time.Hour
)

type Route struct {
	RoomID         string           `json:"room_id"`
	Module         module.ModuleRef `json:"module"`
	ModuleDatabase string           `json:"module_database"`
	Seed           uint64           `json:"seed"`
	CreatedAt      time.Time        `json:"created_at"`
	InviteCode     string           `json:"invite_code,omitempty"`
	InviteDeadline time.Time        `json:"invite_deadline,omitzero"`
}

type State struct {
	Route     Route
	Revision  uint64
	Opaque    module.OpaqueState
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type EventKind string

const (
	EventRoomCreated    EventKind = "room_created"
	EventPlayerJoined   EventKind = "player_joined"
	EventPlayerLeft     EventKind = "player_left"
	EventCommandApplied EventKind = "command_applied"
)

type Event struct {
	Sequence         uint64
	RoomID           string
	Kind             EventKind
	PlayerID         string
	PreviousRevision uint64
	NewRevision      uint64
	Input            module.OpaqueState
	Delta            module.OpaqueState
	StateDigest      [32]byte
	OccurredAt       time.Time
}

type Snapshot struct {
	RoomID   string
	Revision uint64
	State    module.OpaqueState
	SavedAt  time.Time
}

// Store makes a state transition, its event, and an optional periodic snapshot
// one atomic persistence operation.
type Store interface {
	Create(context.Context, State, Event, Snapshot) error
	Load(context.Context, string) (Route, *Snapshot, []Event, error)
	Commit(context.Context, State, Event, *Snapshot) error
	SaveSnapshot(context.Context, Snapshot) error
	Route(context.Context, string) (Route, error)
}

type Command struct {
	PlayerID         string
	ExpectedRevision uint64
	Payload          module.OpaqueState
}

type SnapshotView struct {
	RoomID   string
	Revision uint64
	Module   module.ModuleRef
	View     module.OpaqueState
}

type DeltaView struct {
	RoomID           string
	PreviousRevision uint64
	NewRevision      uint64
	ChangedBy        string
	Module           module.ModuleRef
	View             module.OpaqueState
	NoVisibleChange  bool
}
