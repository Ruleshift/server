package developerapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/Ruleshift/server/internal/module"
	"github.com/Ruleshift/server/internal/roomcore"
)

type roomTestRuntime struct{}

func roomTestTransition() (module.Transition, error) {
	state, _ := module.NewOpaque("example.State", []byte("state"), module.MaxStateBytes)
	delta, _ := module.NewOpaque("example.Delta", []byte("delta"), module.MaxMessageBytes)
	return module.Transition{Changed: true, NextState: state, Delta: delta}, nil
}

func (roomTestRuntime) NewState(context.Context, module.Operation) (module.Transition, error) {
	return roomTestTransition()
}
func (roomTestRuntime) PlayerJoined(context.Context, module.Operation, module.OpaqueState, string) (module.Transition, error) {
	return roomTestTransition()
}
func (roomTestRuntime) PlayerLeft(context.Context, module.Operation, module.OpaqueState, string) (module.Transition, error) {
	return roomTestTransition()
}
func (roomTestRuntime) Apply(context.Context, module.Operation, module.OpaqueState, string, module.OpaqueState) (module.Transition, error) {
	return roomTestTransition()
}
func (roomTestRuntime) ProjectSnapshot(context.Context, module.Operation, module.OpaqueState, module.Viewer) (module.Projection, error) {
	return module.Projection{}, nil
}
func (roomTestRuntime) ProjectDelta(context.Context, module.Operation, module.OpaqueState, module.OpaqueState, module.OpaqueState, module.Viewer) (module.Projection, error) {
	return module.Projection{}, nil
}

func TestCreateRoomPersistsSixCharacterInviteFor24Hours(t *testing.T) {
	ctx := context.Background()
	control := controlplane.NewMemoryStore()
	_, err := control.CreateModule(ctx, controlplane.Module{DeveloperID: "dev", Key: "game", DisplayName: "Game"})
	if err != nil {
		t.Fatal(err)
	}
	version := controlplane.Version{Ref: module.ModuleRef{DeveloperID: "dev", ModuleID: "game", Version: "1.0.0", ImageDigest: "sha256:" + strings.Repeat("a", 64)}}
	if _, _, err = control.PutVersion(ctx, version); err != nil {
		t.Fatal(err)
	}
	if err = control.Activate(ctx, "dev", "game", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	routes := roomcore.NewMemoryStore()
	registry, err := roomcore.NewRegistry(routes, module.ResolverFunc(func(context.Context, module.ModuleRef) (module.Runtime, error) {
		return roomTestRuntime{}, nil
	}), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	createdAt := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	manager := RoomManager{Control: control, Rooms: registry, Routes: routes, Clock: func() time.Time { return createdAt }}

	route, err := manager.CreateRoom(ctx, "dev", "game", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(route.InviteCode) != roomcore.InviteCodeLength {
		t.Fatalf("invite code length = %d, want %d", len(route.InviteCode), roomcore.InviteCodeLength)
	}
	for _, value := range route.InviteCode {
		if !strings.ContainsRune(inviteAlphabet, value) {
			t.Fatalf("invite code %q contains invalid character %q", route.InviteCode, value)
		}
	}
	if !route.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at = %s, want %s", route.CreatedAt, createdAt)
	}
	if want := createdAt.Add(24 * time.Hour); !route.InviteDeadline.Equal(want) {
		t.Fatalf("invite deadline = %s, want %s", route.InviteDeadline, want)
	}
	stored, err := routes.Route(ctx, route.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InviteCode != route.InviteCode || !stored.InviteDeadline.Equal(route.InviteDeadline) {
		t.Fatalf("stored invitation = %q/%s, want %q/%s", stored.InviteCode, stored.InviteDeadline, route.InviteCode, route.InviteDeadline)
	}
}

func TestNewInviteCodeUsesBase36Alphabet(t *testing.T) {
	for range 128 {
		code, err := newInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != roomcore.InviteCodeLength {
			t.Fatalf("invite code length = %d, want %d", len(code), roomcore.InviteCodeLength)
		}
		for _, value := range code {
			if !strings.ContainsRune(inviteAlphabet, value) {
				t.Fatalf("invite code %q contains invalid character %q", code, value)
			}
		}
	}
}
