package room

import "github.com/Ruleshift/server/internal/game"

type StateSnapshot struct {
	RoomID   string
	Revision uint64
	Game     game.Snapshot
}

type ProjectedStateSnapshot struct {
	RoomID   string
	Revision uint64
	Game     game.ViewSnapshot
}

func BuildSnapshot(module game.Module, state RoomState) (StateSnapshot, error) {
	if module == nil {
		return StateSnapshot{}, ErrNilGameModule
	}

	snapshot, err := module.Snapshot(state.GameState)
	if err != nil {
		return StateSnapshot{}, err
	}
	return StateSnapshot{
		RoomID:   state.RoomID,
		Revision: state.Revision,
		Game:     snapshot,
	}, nil
}

func BuildProjectedSnapshot(module game.Module, state RoomState, viewer game.Viewer) (ProjectedStateSnapshot, error) {
	if module == nil {
		return ProjectedStateSnapshot{}, ErrNilGameModule
	}
	snapshot, err := module.ProjectSnapshot(state.GameState, viewer)
	if err != nil {
		return ProjectedStateSnapshot{}, err
	}
	return ProjectedStateSnapshot{RoomID: state.RoomID, Revision: state.Revision, Game: snapshot}, nil
}
