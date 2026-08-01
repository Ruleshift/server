package roomcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/module"
)

type fakeRuntime struct {
	calls int
	setup module.GameSetup
	actor module.Actor
}

func opaque(t *testing.T, typeURL, value string, limit int) module.OpaqueState {
	t.Helper()
	result, err := module.NewOpaque(typeURL, []byte(value), limit)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func transition(value string) (module.Transition, error) {
	state, _ := module.NewOpaque("example.State", []byte(value), module.MaxStateBytes)
	delta, _ := module.NewOpaque("example.Delta", []byte("delta:"+value), module.MaxMessageBytes)
	return module.Transition{Changed: true, NextState: state, Delta: delta}, nil
}
func (f *fakeRuntime) CreateState(_ context.Context, _ module.DeterministicContext, setup module.GameSetup) (module.Transition, error) {
	if setup.PlayerCount == 0 {
		return module.Transition{}, errors.New("missing player count")
	}
	f.setup = setup
	return transition("0")
}
func (f *fakeRuntime) Apply(_ context.Context, _ module.DeterministicContext, state module.OpaqueState, actor module.Actor, command module.OpaqueState) (module.Transition, error) {
	f.calls++
	f.actor = actor
	if string(command.Payload) == "fail" {
		return module.Transition{}, module.ErrUnavailable
	}
	return transition(fmt.Sprintf("%s:%s", state.Payload, command.Payload))
}
func (f *fakeRuntime) ProjectSnapshot(_ context.Context, _ module.DeterministicContext, state module.OpaqueState, _ module.Viewer) (module.Projection, error) {
	view, _ := module.NewOpaque("example.View", state.Payload, module.MaxMessageBytes)
	return module.Projection{Payload: view}, nil
}
func (f *fakeRuntime) ProjectDelta(_ context.Context, _ module.DeterministicContext, _, after, delta module.OpaqueState, _ module.Viewer) (module.Projection, error) {
	view, _ := module.NewOpaque("example.View", append(append([]byte(nil), after.Payload...), delta.Payload...), module.MaxMessageBytes)
	return module.Projection{Payload: view}, nil
}
func testRoute() Route {
	return Route{RoomID: "room-1", Module: module.ModuleRef{DeveloperID: "dev", ModuleID: "counter", Version: "1.0.0", ImageDigest: "sha256:" + strings.Repeat("0", 64)}, ModuleDatabase: "dev_counter", PlayerCount: 1, Seed: 7}
}

func TestModuleFailureDoesNotChangeRevisionAndReplayUsesPinnedVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewMemoryStore()
	implementation := &fakeRuntime{}
	runtime, err := Create(ctx, testRoute(), RuntimeConfig{QueueSize: 4, Store: store, Module: implementation, Clock: func() time.Time { return time.Unix(1, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	go runtime.Run(ctx)
	if _, _, err = runtime.Join(ctx, "p1", false, module.ViewScopePlayer); err != nil {
		t.Fatal(err)
	}
	command := Command{PlayerID: "p1", Payload: opaque(t, "example.Command", "fail", module.MaxMessageBytes)}
	if _, err = runtime.Apply(ctx, command, nil); !errors.Is(err, module.ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
	state, err := runtime.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 0 {
		t.Fatalf("revision changed on module failure: %d", state.Revision)
	}
	command.Payload = opaque(t, "example.Command", "ok", module.MaxMessageBytes)
	if _, err = runtime.Apply(ctx, command, nil); err != nil {
		t.Fatal(err)
	}
	state, err = runtime.State(ctx)
	if err != nil || state.Revision != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	restored, err := Restore(context.Background(), testRoute(), RuntimeConfig{QueueSize: 4, Store: store, Module: &fakeRuntime{}})
	if err != nil {
		t.Fatal(err)
	}
	restoreCtx, restoreCancel := context.WithCancel(context.Background())
	defer restoreCancel()
	go restored.Run(restoreCtx)
	restoredState, err := restored.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if restoredState.Revision != 1 || string(restoredState.Opaque.Payload) != "0:ok" {
		t.Fatalf("unexpected replay state %+v", restoredState)
	}
}

func TestRegistryNeverCreatesRoomOnGet(t *testing.T) {
	store := NewMemoryStore()
	registry, err := NewRegistry(store, module.ResolverFunc(func(context.Context, module.ModuleRef) (module.Runtime, error) { return &fakeRuntime{}, nil }), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if _, err = registry.Get(context.Background(), "missing"); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("Get missing room = %v", err)
	}
}

func TestRegistryResolvesOnlyActiveInviteCode(t *testing.T) {
	store := NewMemoryStore()
	registry, err := NewRegistry(store, module.ResolverFunc(func(context.Context, module.ModuleRef) (module.Runtime, error) { return &fakeRuntime{}, nil }), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	route := testRoute()
	route.InviteCode = "ABC123"
	if _, err = registry.Create(context.Background(), route); err != nil {
		t.Fatal(err)
	}
	roomID, room, err := registry.ResolveInviteCode(context.Background(), route.InviteCode)
	if err != nil || room == nil || roomID != route.RoomID {
		t.Fatalf("ResolveInviteCode active room = %q/%v/%v", roomID, room, err)
	}
	store.mu.Lock()
	store.rooms[route.RoomID].route.InviteDeadline = time.Now().UTC().Add(-time.Second)
	store.mu.Unlock()
	if _, _, err = registry.ResolveInviteCode(context.Background(), route.InviteCode); !errors.Is(err, ErrInviteCodeNotFound) {
		t.Fatalf("ResolveInviteCode expired room = %v, want ErrInviteCodeNotFound", err)
	}
}

func TestCoreOwnsLobbySeatsAndModuleReceivesAuthenticatedActor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewMemoryStore()
	implementation := &fakeRuntime{}
	route := testRoute()
	route.PlayerCount = 2
	runtime, err := Create(ctx, route, RuntimeConfig{QueueSize: 4, Store: store, Module: implementation})
	if err != nil {
		t.Fatal(err)
	}
	if implementation.setup.PlayerCount != 2 {
		t.Fatalf("CreateState setup player_count = %d, want 2", implementation.setup.PlayerCount)
	}
	go runtime.Run(ctx)

	command := Command{PlayerID: "p1", Payload: opaque(t, "example.Command", "before-ready", module.MaxMessageBytes)}
	if _, err = runtime.Apply(ctx, command, nil); !errors.Is(err, ErrMatchNotReady) {
		t.Fatalf("Apply before seats filled = %v, want ErrMatchNotReady", err)
	}
	if implementation.calls != 0 {
		t.Fatalf("module Apply calls before match ready = %d, want 0", implementation.calls)
	}

	firstSnapshot, firstViewer, err := runtime.Join(ctx, "p1", false, module.ViewScopePlayer)
	if err != nil {
		t.Fatal(err)
	}
	if !firstViewer.Seated || firstViewer.SeatIndex != 0 || firstSnapshot.Status != StatusLobby {
		t.Fatalf("first join snapshot=%+v viewer=%+v", firstSnapshot, firstViewer)
	}
	if _, err = runtime.Leave(ctx, firstViewer); err != nil {
		t.Fatal(err)
	}

	_, secondViewer, err := runtime.Join(ctx, "p2", false, module.ViewScopePlayer)
	if err != nil {
		t.Fatal(err)
	}
	if secondViewer.SeatIndex != 0 {
		t.Fatalf("reused lobby seat = %d, want 0", secondViewer.SeatIndex)
	}
	activeSnapshot, firstViewer, err := runtime.Join(ctx, "p1", false, module.ViewScopePlayer)
	if err != nil {
		t.Fatal(err)
	}
	if firstViewer.SeatIndex != 1 || activeSnapshot.Status != StatusActive {
		t.Fatalf("active join snapshot=%+v viewer=%+v", activeSnapshot, firstViewer)
	}

	if _, err = runtime.Leave(ctx, secondViewer); err != nil {
		t.Fatal(err)
	}
	reconnectedSnapshot, reconnectedViewer, err := runtime.Join(ctx, "p2", false, module.ViewScopePlayer)
	if err != nil {
		t.Fatal(err)
	}
	if reconnectedViewer.SeatIndex != 0 || reconnectedSnapshot.Status != StatusActive {
		t.Fatalf("reconnect snapshot=%+v viewer=%+v", reconnectedSnapshot, reconnectedViewer)
	}
	if _, _, err = runtime.Join(ctx, "p3", false, module.ViewScopePlayer); !errors.Is(err, ErrRoomFull) {
		t.Fatalf("third player join = %v, want ErrRoomFull", err)
	}

	command = Command{PlayerID: "p1", Payload: opaque(t, "example.Command", "after-ready", module.MaxMessageBytes)}
	if _, err = runtime.Apply(ctx, command, nil); err != nil {
		t.Fatal(err)
	}
	if implementation.actor.PlayerID != "p1" || implementation.actor.SeatIndex != 1 {
		t.Fatalf("module actor = %+v, want authenticated p1 at seat 1", implementation.actor)
	}
	state, err := runtime.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 1 || state.Status != StatusActive || len(state.Participants) != 2 {
		t.Fatalf("state after command = %+v", state)
	}
}
