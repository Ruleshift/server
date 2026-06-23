package hiddennumber_test

import (
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/hiddennumber"
	"github.com/Ruleshift/server/internal/room"
)

func TestProjectionScopesAndProtoPresence(t *testing.T) {
	module := hiddennumber.NewModule()
	state, err := module.NewState(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, changed, err := module.PlayerJoined(state, "player-a")
	if err != nil || !changed {
		t.Fatalf("join player-a: changed=%t err=%v", changed, err)
	}
	state, changed, err = module.PlayerJoined(state, "player-b")
	if err != nil || !changed {
		t.Fatalf("join player-b: changed=%t err=%v", changed, err)
	}
	state, _, err = module.Apply(state, game.Command{PlayerID: "player-a", Type: game.CommandSetSecret, Payload: hiddennumber.SetSecret{Value: 123456}, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		viewer     game.Viewer
		wantSecret bool
	}{
		{"owner", game.Viewer{PlayerID: "player-a", JoinMode: game.JoinModePlayer, Scope: game.ViewScopePlayer}, true},
		{"opponent", game.Viewer{PlayerID: "player-b", JoinMode: game.JoinModePlayer, Scope: game.ViewScopePlayer}, false},
		{"public spectator", game.Viewer{PlayerID: "watcher", JoinMode: game.JoinModeSpectator, Scope: game.ViewScopePublic}, false},
		{"trusted spectator", game.Viewer{PlayerID: "caster", JoinMode: game.JoinModeSpectator, Scope: game.ViewScopeFull}, true},
		{"unspecified fail closed", game.Viewer{PlayerID: "unknown"}, false},
		{"full player fail closed", game.Viewer{PlayerID: "player-b", JoinMode: game.JoinModePlayer, Scope: game.ViewScopeFull}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projected, err := module.ProjectSnapshot(state, tc.viewer)
			if err != nil {
				t.Fatal(err)
			}
			payload := projected.Payload.(hiddennumber.SnapshotView)
			if got := payload.Players[0].Secret != nil; got != tc.wantSecret {
				t.Fatalf("private value presence=%t want=%t", got, tc.wantSecret)
			}
			envelope := room.SnapshotEnvelope(room.ProjectedStateSnapshot{RoomID: "room", Revision: 3, Game: projected})
			protoPlayer := envelope.GetStateSnapshot().GetHiddenNumber().GetPlayers()[0]
			if got := protoPlayer.Secret != nil; got != tc.wantSecret {
				t.Fatalf("protobuf secret presence=%t want=%t", got, tc.wantSecret)
			}
			if !tc.wantSecret && protoPlayer.GetSecret() != 0 {
				t.Fatalf("unauthorized secret=%d", protoPlayer.GetSecret())
			}
		})
	}
}

func TestProjectDeltaUsesNoVisibleChange(t *testing.T) {
	module := hiddennumber.NewModule()
	state, _ := module.NewState(time.Now())
	state, _, _ = module.PlayerJoined(state, "player-a")
	state, _, _ = module.PlayerJoined(state, "player-b")
	first, firstDelta, err := module.Apply(state, game.Command{PlayerID: "player-a", Type: game.CommandSetSecret, Payload: hiddennumber.SetSecret{Value: 10}, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	opponent := game.Viewer{PlayerID: "player-b", JoinMode: game.JoinModePlayer, Scope: game.ViewScopePlayer}
	visible, err := module.ProjectDelta(state, first, firstDelta, opponent)
	if err != nil {
		t.Fatal(err)
	}
	if visible.NoVisibleChange {
		t.Fatal("first has_secret transition must be visible")
	}
	if visible.Payload.(hiddennumber.DeltaView).Secret != nil {
		t.Fatal("opponent delta leaked secret")
	}

	second, secondDelta, err := module.Apply(first, game.Command{PlayerID: "player-a", Type: game.CommandSetSecret, Payload: hiddennumber.SetSecret{Value: 20}, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := module.ProjectDelta(first, second, secondDelta, opponent)
	if err != nil {
		t.Fatal(err)
	}
	if !hidden.NoVisibleChange || hidden.Payload != nil {
		t.Fatalf("repeat projection=%#v, want hidden revision advance", hidden)
	}
	owner := game.Viewer{PlayerID: "player-a", JoinMode: game.JoinModePlayer, Scope: game.ViewScopePlayer}
	ownerDelta, err := module.ProjectDelta(first, second, secondDelta, owner)
	if err != nil {
		t.Fatal(err)
	}
	if ownerDelta.NoVisibleChange || ownerDelta.Payload.(hiddennumber.DeltaView).Secret == nil {
		t.Fatal("owner did not receive changed secret")
	}
}

func TestPlayerJoinCapacityAndCanonicalHash(t *testing.T) {
	module := hiddennumber.NewModule()
	state, _ := module.NewState(time.Now())
	state, _, _ = module.PlayerJoined(state, "a")
	state, _, _ = module.PlayerJoined(state, "b")
	if _, _, err := module.PlayerJoined(state, "c"); err != game.ErrRoomFull {
		t.Fatalf("third player error=%v want ErrRoomFull", err)
	}
	canonical, err := module.Snapshot(state)
	if err != nil || canonical.StateHash == 0 {
		t.Fatalf("canonical snapshot hash=%d err=%v", canonical.StateHash, err)
	}
}

func TestCanonicalReplayRestoresSecret(t *testing.T) {
	module := hiddennumber.NewModule()
	now := time.Now().UTC()
	gameState, _ := module.NewState(now)
	state := room.NewState("replay-room", module.Type(), gameState, now)
	events := []room.RoomEvent{room.NewRoomCreatedEvent(state)}
	events[0].Sequence = 1
	for _, playerID := range []string{"a", "b"} {
		before := state
		nextGameState, changed, err := module.PlayerJoined(state.GameState, playerID)
		if err != nil || !changed {
			t.Fatalf("join %s: changed=%t err=%v", playerID, changed, err)
		}
		state.GameState = nextGameState
		state.Revision++
		state.UpdatedAt = now
		snapshot, _ := room.BuildSnapshot(module, state)
		event := room.NewPlayerJoinedEvent(before, state, snapshot, playerID, now)
		event.Sequence = uint64(len(events) + 1)
		events = append(events, event)
	}
	next, delta, err := room.ApplyGameCommand(module, state, room.GameCommand{RoomID: state.RoomID, PlayerID: "a", Type: game.CommandSetSecret, Payload: hiddennumber.SetSecret{Value: 777}, ReceivedAt: now}, now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := room.NewGameCommandEvent(delta)
	if err != nil {
		t.Fatal(err)
	}
	event.Sequence = uint64(len(events) + 1)
	events = append(events, event)
	replayed, err := room.ReplayEvents(module, events)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := room.BuildSnapshot(module, next)
	got, _ := room.BuildSnapshot(module, replayed)
	if got.Revision != want.Revision || got.Game.StateHash != want.Game.StateHash {
		t.Fatalf("replayed revision/hash=%d/%d want=%d/%d", got.Revision, got.Game.StateHash, want.Revision, want.Game.StateHash)
	}
	trusted, err := module.ProjectSnapshot(replayed.GameState, game.Viewer{PlayerID: "caster", JoinMode: game.JoinModeSpectator, Scope: game.ViewScopeFull})
	if err != nil {
		t.Fatal(err)
	}
	secret := trusted.Payload.(hiddennumber.SnapshotView).Players[0].Secret
	if secret == nil || *secret != 777 {
		t.Fatalf("replayed secret=%v want 777", secret)
	}
}

func BenchmarkProjectSnapshot(b *testing.B) {
	module := hiddennumber.NewModule()
	state, _ := module.NewState(time.Now())
	state, _, _ = module.PlayerJoined(state, "a")
	state, _, _ = module.PlayerJoined(state, "b")
	state, _, _ = module.Apply(state, game.Command{PlayerID: "a", Type: game.CommandSetSecret, Payload: hiddennumber.SetSecret{Value: 42}, At: time.Now()})
	viewers := []game.Viewer{
		{PlayerID: "a", JoinMode: game.JoinModePlayer, Scope: game.ViewScopePlayer},
		{PlayerID: "b", JoinMode: game.JoinModePlayer, Scope: game.ViewScopePlayer},
		{PlayerID: "watcher", JoinMode: game.JoinModeSpectator, Scope: game.ViewScopePublic},
		{PlayerID: "caster", JoinMode: game.JoinModeSpectator, Scope: game.ViewScopeFull},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := module.ProjectSnapshot(state, viewers[i%len(viewers)]); err != nil {
			b.Fatal(err)
		}
	}
}
