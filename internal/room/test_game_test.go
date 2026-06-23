package room

import (
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/xiangqi"
)

type testGameModule struct{}

type testGameState struct {
	moves uint64
}

func (testGameModule) Type() game.Type {
	return game.TypeXiangqi
}

func (testGameModule) NewState(time.Time) (any, error) {
	return &testGameState{}, nil
}

func (testGameModule) PlayerJoined(state any, _ string) (any, bool, error) {
	return state, false, nil
}

func (m testGameModule) ProjectSnapshot(state any, _ game.Viewer) (game.ViewSnapshot, error) {
	snapshot, err := m.Snapshot(state)
	if err != nil {
		return game.ViewSnapshot{}, err
	}
	return game.ViewSnapshot{Type: snapshot.Type, Status: snapshot.Status, ViewHash: snapshot.StateHash, Payload: snapshot.Payload}, nil
}

func (testGameModule) ProjectDelta(_ any, _ any, delta game.Delta, _ game.Viewer) (game.ViewDelta, error) {
	return game.ViewDelta{Type: delta.Type, CommandType: delta.CommandType, Status: delta.Status, ViewHash: delta.StateHash, Payload: delta.Payload}, nil
}

func (testGameModule) Snapshot(state any) (game.Snapshot, error) {
	testState, ok := state.(*testGameState)
	if !ok {
		return game.Snapshot{}, game.ErrUnsupportedState
	}
	base := game.Snapshot{
		Type:      game.TypeXiangqi,
		Status:    game.StatusActive,
		StateHash: testState.moves,
	}
	base.Payload = xiangqi.Snapshot{
		Snapshot:   base,
		FEN:        fmt.Sprintf("test:%d", testState.moves),
		Board:      []uint32{uint32(testState.moves)},
		SideToMove: xiangqi.SideRed,
	}
	return base, nil
}

func (testGameModule) Apply(state any, command game.Command) (any, game.Delta, error) {
	testState, ok := state.(*testGameState)
	if !ok {
		return state, game.Delta{}, game.ErrUnsupportedState
	}
	if command.Type != game.CommandDoMove {
		return state, game.Delta{}, game.ErrInvalidCommand
	}

	move, ok := command.Payload.(xiangqi.Move)
	if !ok {
		return state, game.Delta{}, game.ErrInvalidCommand
	}
	nextState := &testGameState{moves: testState.moves + 1}
	base := game.Delta{
		Type:        game.TypeXiangqi,
		CommandType: game.CommandDoMove,
		Status:      game.StatusActive,
		StateHash:   nextState.moves,
	}
	base.CommandPayload = move
	base.Payload = xiangqi.Delta{
		Delta:      base,
		MoveUCI:    move.UCI,
		FromSquare: move.FromSquare,
		ToSquare:   move.ToSquare,
		SquareUpdates: []xiangqi.SquareUpdate{
			{Square: move.FromSquare, Piece: uint32(nextState.moves)},
		},
		SideToMove: xiangqi.SideRed,
	}
	return nextState, base, nil
}
