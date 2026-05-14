package room

import "fmt"

func ReplayEvents(events []RoomEvent) (RoomState, error) {
	if len(events) == 0 {
		return RoomState{}, fmt.Errorf("replay events: no events")
	}

	var state RoomState
	var initialized bool
	var lastSequence uint64

	for _, event := range events {
		if event.Sequence == 0 {
			return state, fmt.Errorf("replay event missing sequence: type=%s room=%q", event.Type, event.RoomID)
		}
		if lastSequence != 0 && event.Sequence <= lastSequence {
			return state, fmt.Errorf("replay event sequence regression: previous=%d current=%d", lastSequence, event.Sequence)
		}
		lastSequence = event.Sequence

		if event.RoomID == "" {
			return state, ErrEmptyRoomID
		}

		if event.Type == EventTypeRoomCreated {
			if initialized {
				return state, fmt.Errorf("replay duplicate room created event: room=%q", event.RoomID)
			}
			state = NewState(event.RoomID, event.OccurredAt)
			state.Value = event.NewValue
			state.Revision = event.Revision
			state.UpdatedAt = event.OccurredAt
			initialized = true
			continue
		}

		if !initialized {
			return state, fmt.Errorf("replay event before room created: type=%s room=%q", event.Type, event.RoomID)
		}
		if event.RoomID != state.RoomID {
			return state, fmt.Errorf("replay room mismatch: event=%q state=%q", event.RoomID, state.RoomID)
		}

		switch event.Type {
		case EventTypeIntAdded, EventTypeIntSet:
			next, err := replayIntEvent(state, event)
			if err != nil {
				return state, err
			}
			state = next
		case EventTypePlayerJoined, EventTypeSnapshotSent, EventTypePlayerDisconnected:
			continue
		default:
			return state, fmt.Errorf("replay unknown event type: %s", event.Type)
		}
	}

	return state, nil
}

func replayIntEvent(state RoomState, event RoomEvent) (RoomState, error) {
	if event.PreviousRevision != state.Revision {
		return state, fmt.Errorf("replay revision gap: expected previous=%d got=%d", state.Revision, event.PreviousRevision)
	}
	if event.PreviousValue != state.Value {
		return state, fmt.Errorf("replay value gap: expected previous=%d got=%d", state.Value, event.PreviousValue)
	}
	if event.NewRevision != event.PreviousRevision+1 {
		return state, fmt.Errorf("replay invalid new revision: previous=%d new=%d", event.PreviousRevision, event.NewRevision)
	}
	if event.Type == EventTypeIntAdded && event.NewValue != event.PreviousValue+event.Operand {
		return state, fmt.Errorf("replay invalid add event: previous=%d operand=%d new=%d", event.PreviousValue, event.Operand, event.NewValue)
	}
	if event.Type == EventTypeIntSet && event.NewValue != event.Operand {
		return state, fmt.Errorf("replay invalid set event: operand=%d new=%d", event.Operand, event.NewValue)
	}

	state.Value = event.NewValue
	state.Revision = event.NewRevision
	state.UpdatedAt = event.OccurredAt
	return state, nil
}

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
