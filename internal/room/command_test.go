package room

import (
	"errors"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/xiangqi"
)

func TestApplyGameCommandMove(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	module := testGameModule{}
	gameState, err := module.NewState(now)
	if err != nil {
		t.Fatalf("NewState returned error: %v", err)
	}
	state := NewState("room-1", module.Type(), gameState, now)

	next, delta, err := ApplyGameCommand(module, state, GameCommand{
		RoomID:   "room-1",
		PlayerID: "player-1",
		Type:     game.CommandDoMove,
		Payload:  xiangqi.Move{FromSquare: 1, ToSquare: 2, UCI: "a0a1"},
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApplyGameCommand returned error: %v", err)
	}

	if next.Revision != 1 {
		t.Fatalf("Revision = %d, want 1", next.Revision)
	}
	if delta.PreviousRevision != 0 || delta.NewRevision != 1 {
		t.Fatalf("delta revisions = %d -> %d, want 0 -> 1", delta.PreviousRevision, delta.NewRevision)
	}
	if delta.Game.StateHash != 1 {
		t.Fatalf("delta state hash = %d, want 1", delta.Game.StateHash)
	}
}

func TestApplyGameCommandRejectsInvalidCommand(t *testing.T) {
	state := newTestRoomState(t)

	_, _, err := ApplyGameCommand(testGameModule{}, state, GameCommand{
		RoomID:   "room-1",
		PlayerID: "player-1",
		Type:     game.CommandUnspecified,
	}, time.Unix(101, 0).UTC())
	if !errors.Is(err, game.ErrInvalidCommand) {
		t.Fatalf("error = %v, want game.ErrInvalidCommand", err)
	}
}

func TestApplyGameCommandRejectsRevisionMismatch(t *testing.T) {
	state := newTestRoomState(t)
	state.Revision = 9

	_, _, err := ApplyGameCommand(testGameModule{}, state, GameCommand{
		RoomID:           "room-1",
		PlayerID:         "player-1",
		Type:             game.CommandDoMove,
		Payload:          xiangqi.Move{UCI: "a0a1"},
		ExpectedRevision: 8,
	}, time.Unix(101, 0).UTC())
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("error = %v, want ErrRevisionMismatch", err)
	}
}

func newTestRoomState(t *testing.T) RoomState {
	t.Helper()

	now := time.Unix(100, 0).UTC()
	module := testGameModule{}
	gameState, err := module.NewState(now)
	if err != nil {
		t.Fatalf("NewState returned error: %v", err)
	}
	return NewState("room-1", module.Type(), gameState, now)
}
