package room

import "fmt"

func ReplayDeltas(initial RoomState, deltas []StateDelta) (RoomState, error) {
	state := initial
	for _, delta := range deltas {
		if delta.RoomID != state.RoomID {
			return state, fmt.Errorf("replay room mismatch: delta=%q state=%q", delta.RoomID, state.RoomID)
		}
		if delta.PreviousRevision != state.Revision {
			return state, fmt.Errorf("replay revision gap: expected previous=%d got=%d", state.Revision, delta.PreviousRevision)
		}
		state.Value = delta.NewValue
		state.Revision = delta.NewRevision
		state.UpdatedAt = delta.AppliedAt
	}
	return state, nil
}
