package gatewayv2

import (
	"context"
	"strings"
	"testing"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/metrics"
	"github.com/Ruleshift/server/internal/module"
	ruleshiftv2 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv2"
	"github.com/Ruleshift/server/internal/roomcore"
)

type inviteTestRuntime struct{}

func inviteTestOpaque(typeURL, value string, limit int) module.OpaqueState {
	result, _ := module.NewOpaque(typeURL, []byte(value), limit)
	return result
}

func (inviteTestRuntime) CreateState(context.Context, module.DeterministicContext, module.GameSetup) (module.Transition, error) {
	return module.Transition{
		Changed:   true,
		NextState: inviteTestOpaque("example.State", "state", module.MaxStateBytes),
		Delta:     inviteTestOpaque("example.Delta", "created", module.MaxMessageBytes),
	}, nil
}
func (inviteTestRuntime) Apply(context.Context, module.DeterministicContext, module.OpaqueState, module.Actor, module.OpaqueState) (module.Transition, error) {
	return module.Transition{}, nil
}
func (inviteTestRuntime) ProjectSnapshot(context.Context, module.DeterministicContext, module.OpaqueState, module.Viewer) (module.Projection, error) {
	return module.Projection{Payload: inviteTestOpaque("example.View", "view", module.MaxMessageBytes)}, nil
}
func (inviteTestRuntime) ProjectDelta(context.Context, module.DeterministicContext, module.OpaqueState, module.OpaqueState, module.OpaqueState, module.Viewer) (module.Projection, error) {
	return module.Projection{Payload: inviteTestOpaque("example.View", "view", module.MaxMessageBytes)}, nil
}

func TestJoinResolvesInviteCodeToInternalRoomID(t *testing.T) {
	ctx := context.Background()
	store := roomcore.NewMemoryStore()
	registry, err := roomcore.NewRegistry(store, module.ResolverFunc(func(context.Context, module.ModuleRef) (module.Runtime, error) {
		return inviteTestRuntime{}, nil
	}), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	route := roomcore.Route{
		RoomID:         "room-internal-id",
		Module:         module.ModuleRef{DeveloperID: "dev", ModuleID: "game", Version: "1.0.0", ImageDigest: "sha256:" + strings.Repeat("a", 64)},
		ModuleDatabase: "dev_game",
		InviteCode:     "ABC123",
		PlayerCount:    1,
	}
	if _, err = registry.Create(ctx, route); err != nil {
		t.Fatal(err)
	}
	gateway := &Gateway{rooms: registry, metrics: metrics.NopRecorder{}, hubs: map[string]*hub{}}
	state := connection{identity: &auth.Identity{PlayerID: "p1"}, session: newSession("p1", 4)}
	request := &ruleshiftv2.JoinRoomRequest{InviteCode: route.InviteCode, JoinMode: ruleshiftv2.JoinMode_JOIN_MODE_SPECTATOR}

	if err = gateway.join(ctx, &state, request); err != nil {
		t.Fatal(err)
	}
	if state.room == nil || state.roomID != route.RoomID {
		t.Fatalf("joined room = %q/%v, want %q/non-nil", state.roomID, state.room, route.RoomID)
	}
}
