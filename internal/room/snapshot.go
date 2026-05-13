package room

type StateSnapshot struct {
	RoomID   string
	Value    int64
	Revision uint64
}

func BuildSnapshot(state RoomState) StateSnapshot {
	return StateSnapshot{
		RoomID:   state.RoomID,
		Value:    state.Value,
		Revision: state.Revision,
	}
}
