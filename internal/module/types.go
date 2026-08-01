// Package module contains the language-neutral contract used by the Ruleshift
// core. It deliberately has no imports from game implementations.
package module

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/anypb"
)

const (
	ABIVersion            uint32 = 2
	MaxStateBytes                = 1 << 20
	MaxMessageBytes              = 256 << 10
	DefaultDeadline              = 50 * time.Millisecond
	CreateStateDeadline          = 250 * time.Millisecond
	MaxTransitionDeadline        = 250 * time.Millisecond
	MaxPlayers            uint32 = 64
)

var (
	ErrUnavailable       = errors.New("module unavailable")
	ErrCommandRejected   = errors.New("command rejected")
	ErrProtocolViolation = errors.New("module protocol violation")
)

type ModuleRef struct {
	DeveloperID string `json:"developer_id"`
	ModuleID    string `json:"module_id"`
	Version     string `json:"version"`
	ImageDigest string `json:"image_digest"`
}

func (r ModuleRef) Validate() error {
	if r.DeveloperID == "" || r.ModuleID == "" || r.Version == "" {
		return fmt.Errorf("module reference requires developer_id, module_id and version")
	}
	if len(r.ImageDigest) != len("sha256:")+sha256.Size*2 || r.ImageDigest[:7] != "sha256:" {
		return fmt.Errorf("image digest must be an immutable sha256 digest")
	}
	return nil
}

type OpaqueState struct {
	TypeURL string
	Payload []byte
	Digest  [sha256.Size]byte
}

func NewOpaque(typeURL string, payload []byte, limit int) (OpaqueState, error) {
	if typeURL == "" {
		return OpaqueState{}, fmt.Errorf("%w: protobuf type URL is empty", ErrProtocolViolation)
	}
	if len(payload) > limit {
		return OpaqueState{}, fmt.Errorf("%w: payload is %d bytes; maximum is %d", ErrProtocolViolation, len(payload), limit)
	}
	copyPayload := append([]byte(nil), payload...)
	return OpaqueState{TypeURL: typeURL, Payload: copyPayload, Digest: sha256.Sum256(copyPayload)}, nil
}

func StateFromAny(value *anypb.Any) (OpaqueState, error) {
	if value == nil {
		return OpaqueState{}, fmt.Errorf("%w: state is nil", ErrProtocolViolation)
	}
	return NewOpaque(value.TypeUrl, value.Value, MaxStateBytes)
}

func MessageFromAny(value *anypb.Any) (OpaqueState, error) {
	if value == nil {
		return OpaqueState{}, fmt.Errorf("%w: message is nil", ErrProtocolViolation)
	}
	return NewOpaque(value.TypeUrl, value.Value, MaxMessageBytes)
}

func (s OpaqueState) Any() *anypb.Any {
	return &anypb.Any{TypeUrl: s.TypeURL, Value: append([]byte(nil), s.Payload...)}
}

type ViewScope uint8

const (
	ViewScopePlayer ViewScope = iota + 1
	ViewScopePublic
	ViewScopeFull
)

type Viewer struct {
	PlayerID  string
	SeatIndex uint32
	Seated    bool
	Scope     ViewScope
}

type Actor struct {
	PlayerID  string
	SeatIndex uint32
}

type GameSetup struct {
	PlayerCount uint32
}

type DeterministicContext struct {
	Revision uint64
	Now      time.Time
	Seed     uint64
}

type Transition struct {
	Changed   bool
	NextState OpaqueState
	Delta     OpaqueState
}

type Projection struct {
	Payload         OpaqueState
	NoVisibleChange bool
}

type Runtime interface {
	CreateState(context.Context, DeterministicContext, GameSetup) (Transition, error)
	Apply(context.Context, DeterministicContext, OpaqueState, Actor, OpaqueState) (Transition, error)
	ProjectSnapshot(context.Context, DeterministicContext, OpaqueState, Viewer) (Projection, error)
	ProjectDelta(context.Context, DeterministicContext, OpaqueState, OpaqueState, OpaqueState, Viewer) (Projection, error)
}

type Resolver interface {
	Resolve(context.Context, ModuleRef) (Runtime, error)
}

type ResolverFunc func(context.Context, ModuleRef) (Runtime, error)

func (f ResolverFunc) Resolve(ctx context.Context, ref ModuleRef) (Runtime, error) {
	return f(ctx, ref)
}
