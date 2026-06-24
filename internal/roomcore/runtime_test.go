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

type fakeRuntime struct{ calls int }

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
func (f *fakeRuntime) NewState(_ context.Context, op module.Operation) (module.Transition, error) {
	if op.OperationID == "" {
		return module.Transition{}, errors.New("missing operation")
	}
	return transition("0")
}
func (f *fakeRuntime) PlayerJoined(_ context.Context, _ module.Operation, state module.OpaqueState, player string) (module.Transition, error) {
	return transition(string(state.Payload) + "+" + player)
}
func (f *fakeRuntime) PlayerLeft(_ context.Context, _ module.Operation, state module.OpaqueState, player string) (module.Transition, error) {
	return transition(string(state.Payload) + "-" + player)
}
func (f *fakeRuntime) Apply(_ context.Context, _ module.Operation, state module.OpaqueState, _ string, command module.OpaqueState) (module.Transition, error) {
	f.calls++
	if string(command.Payload) == "fail" {
		return module.Transition{}, module.ErrUnavailable
	}
	return transition(fmt.Sprintf("%s:%s", state.Payload, command.Payload))
}
func (f *fakeRuntime) ProjectSnapshot(_ context.Context, _ module.Operation, state module.OpaqueState, _ module.Viewer) (module.Projection, error) {
	view, _ := module.NewOpaque("example.View", state.Payload, module.MaxMessageBytes)
	return module.Projection{Payload: view}, nil
}
func (f *fakeRuntime) ProjectDelta(_ context.Context, _ module.Operation, _, after, delta module.OpaqueState, _ module.Viewer) (module.Projection, error) {
	view, _ := module.NewOpaque("example.View", append(append([]byte(nil), after.Payload...), delta.Payload...), module.MaxMessageBytes)
	return module.Projection{Payload: view}, nil
}
func testRoute() Route {
	return Route{RoomID: "room-1", Module: module.ModuleRef{DeveloperID: "dev", ModuleID: "counter", Version: "1.0.0", ImageDigest: "sha256:" + strings.Repeat("0", 64)}, ModuleDatabase: "dev_counter", Seed: 7}
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
