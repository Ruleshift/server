package room

import "time"

type RoomState struct {
	RoomID    string
	Value     int64
	Revision  uint64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewState(roomID string, now time.Time) RoomState {
	return RoomState{
		RoomID:    roomID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
