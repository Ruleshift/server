package room

import (
	"time"

	"github.com/Ruleshift/server/internal/game"
)

type RoomState struct {
	RoomID    string
	GameType  game.Type
	GameState any
	Revision  uint64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewState(roomID string, gameType game.Type, gameState any, now time.Time) RoomState {
	return RoomState{
		RoomID:    roomID,
		GameType:  gameType,
		GameState: gameState,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
