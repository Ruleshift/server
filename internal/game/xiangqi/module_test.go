package xiangqi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/game"
)

func TestModuleAppliesLegalMovesAndSeatsPlayers(t *testing.T) {
	module := NewModule()
	state, err := module.NewState(time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("NewState returned error: %v", err)
	}
	state, _, err = module.PlayerJoined(state, "player-red")
	if err != nil {
		t.Fatalf("join red returned error: %v", err)
	}
	state, _, err = module.PlayerJoined(state, "player-black")
	if err != nil {
		t.Fatalf("join black returned error: %v", err)
	}

	snapshot, err := module.Snapshot(state)
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	xSnapshot, ok := SnapshotPayload(snapshot)
	if !ok {
		t.Fatalf("snapshot payload = %T, want xiangqi.Snapshot", snapshot.Payload)
	}
	if xSnapshot.RedPlayerID != "player-red" || xSnapshot.BlackPlayerID != "player-black" {
		t.Fatalf("seats = red %q black %q", xSnapshot.RedPlayerID, xSnapshot.BlackPlayerID)
	}

	state, delta, err := module.Apply(state, game.Command{
		PlayerID: "player-red",
		Type:     game.CommandDoMove,
		Payload:  Move{UCI: "h2e2"},
		At:       time.Unix(101, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("red move returned error: %v", err)
	}
	xDelta, ok := DeltaPayload(delta)
	if !ok {
		t.Fatalf("delta payload = %T, want xiangqi.Delta", delta.Payload)
	}
	if delta.CommandType != game.CommandDoMove || xDelta.MoveUCI != "h2e2" || xDelta.SideToMove != SideBlack {
		t.Fatalf("delta after red move = %#v", delta)
	}
	if len(xDelta.SquareUpdates) != 2 {
		t.Fatalf("square updates = %d, want 2", len(xDelta.SquareUpdates))
	}

	_, _, err = module.Apply(state, game.Command{
		PlayerID: "player-red",
		Type:     game.CommandDoMove,
		Payload:  Move{UCI: "h7e7"},
		At:       time.Unix(102, 0).UTC(),
	})
	if !errors.Is(err, game.ErrNotPlayersTurn) {
		t.Fatalf("same-player second move error = %v, want ErrNotPlayersTurn", err)
	}

	state, delta, err = module.Apply(state, game.Command{
		PlayerID: "player-black",
		Type:     game.CommandDoMove,
		Payload:  Move{UCI: "h7e7"},
		At:       time.Unix(103, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("black move returned error: %v", err)
	}
	xDelta, ok = DeltaPayload(delta)
	if !ok {
		t.Fatalf("delta payload = %T, want xiangqi.Delta", delta.Payload)
	}
	if xDelta.SideToMove != SideRed {
		t.Fatalf("side to move = %d, want red", xDelta.SideToMove)
	}

	_, delta, err = module.Apply(state, game.Command{
		PlayerID: "player-red",
		Type:     game.CommandResign,
		At:       time.Unix(104, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("resign returned error: %v", err)
	}
	xDelta, ok = DeltaPayload(delta)
	if !ok {
		t.Fatalf("delta payload = %T, want xiangqi.Delta", delta.Payload)
	}
	if delta.Status != game.StatusResigned || xDelta.WinnerPlayerID != "player-black" {
		t.Fatalf("resign delta = %#v", delta)
	}
}

func TestModuleDatabaseDefinitionAndPayloadCodec(t *testing.T) {
	module := NewModule()
	definition := module.DatabaseDefinition()
	if definition.Name != "xiangqi" || len(definition.Migrations) == 0 {
		t.Fatalf("database definition = %#v, want named schema with migrations", definition)
	}

	want := Move{FromSquare: 1, ToSquare: 2, UCI: "a0a1"}
	encoded, err := module.MarshalCommandPayload(context.Background(), game.CommandDoMove, want)
	if err != nil {
		t.Fatalf("MarshalCommandPayload returned error: %v", err)
	}
	decoded, err := module.UnmarshalCommandPayload(context.Background(), game.CommandDoMove, encoded)
	if err != nil {
		t.Fatalf("UnmarshalCommandPayload returned error: %v", err)
	}
	if got, ok := decoded.(Move); !ok || got != want {
		t.Fatalf("decoded payload = %#v, want %#v", decoded, want)
	}
}

func TestModuleDoesNotMutateUncommittedState(t *testing.T) {
	module := NewModule()
	original, err := module.NewState(time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("NewState returned error: %v", err)
	}
	joined, _, err := module.PlayerJoined(original, "player-red")
	if err != nil {
		t.Fatalf("PlayerJoined returned error: %v", err)
	}
	originalSnapshot, err := module.Snapshot(original)
	if err != nil {
		t.Fatalf("original Snapshot returned error: %v", err)
	}
	originalPayload, _ := SnapshotPayload(originalSnapshot)
	if originalPayload.RedPlayerID != "" {
		t.Fatalf("original red player = %q, want empty", originalPayload.RedPlayerID)
	}

	beforeMove := joined
	_, _, err = module.Apply(joined, game.Command{
		PlayerID: "player-red",
		Type:     game.CommandDoMove,
		Payload:  Move{UCI: "h2e2"},
		At:       time.Unix(101, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	beforeSnapshot, err := module.Snapshot(beforeMove)
	if err != nil {
		t.Fatalf("before-move Snapshot returned error: %v", err)
	}
	beforePayload, _ := SnapshotPayload(beforeSnapshot)
	if beforePayload.FEN != initialFEN {
		t.Fatalf("uncommitted input FEN = %q, want initial FEN", beforePayload.FEN)
	}
}
