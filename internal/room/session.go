package room

import (
	"context"
	"errors"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/xiangqi"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

const (
	CloseReasonSlowConsumer = "slow_consumer"
	CloseReasonShutdown     = "shutdown"
	CloseReasonReplaced     = "replaced"
	CloseReasonDisconnected = "disconnected"
)

var (
	ErrPlayerSinkFull   = errors.New("player sink send queue is full")
	ErrPlayerSinkClosed = errors.New("player sink is closed")
)

type PlayerSink interface {
	SessionID() uint64
	PlayerID() string
	Send(ctx context.Context, msg *ruleshiftv1.ServerEnvelope) error
	Close(reason string)
}

func SnapshotEnvelope(snapshot StateSnapshot) *ruleshiftv1.ServerEnvelope {
	state := &ruleshiftv1.StateSnapshot{
		RoomId:   snapshot.RoomID,
		Revision: snapshot.Revision,
		GameType: toProtoGameType(snapshot.Game.Type),
	}
	if snapshot.Game.Type == game.TypeXiangqi {
		state.State = &ruleshiftv1.StateSnapshot_Xiangqi{Xiangqi: toProtoXiangqiSnapshot(snapshot.Game)}
	}
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateSnapshot{StateSnapshot: state}}
}

func DeltaEnvelope(delta StateDelta) *ruleshiftv1.ServerEnvelope {
	stateDelta := &ruleshiftv1.StateDelta{
		RoomId:            delta.RoomID,
		PreviousRevision:  delta.PreviousRevision,
		NewRevision:       delta.NewRevision,
		ChangedByPlayerId: delta.ChangedByPlayerID,
		GameType:          toProtoGameType(delta.Game.Type),
	}
	if delta.Game.Type == game.TypeXiangqi {
		stateDelta.Delta = &ruleshiftv1.StateDelta_Xiangqi{Xiangqi: toProtoXiangqiDelta(delta.Game)}
	}
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateDelta{StateDelta: stateDelta}}
}

func toProtoXiangqiSnapshot(snapshot game.Snapshot) *ruleshiftv1.XiangqiSnapshot {
	payload, _ := xiangqi.SnapshotPayload(snapshot)
	return &ruleshiftv1.XiangqiSnapshot{
		Fen:                   payload.FEN,
		Board:                 payload.Board,
		SideToMove:            toProtoSide(payload.SideToMove),
		Status:                toProtoStatus(snapshot.Status),
		RedPlayerId:           payload.RedPlayerID,
		BlackPlayerId:         payload.BlackPlayerID,
		WinnerPlayerId:        payload.WinnerPlayerID,
		DrawOfferedByPlayerId: payload.DrawOfferedByPlayerID,
		StateHash:             snapshot.StateHash,
	}
}

func toProtoXiangqiDelta(delta game.Delta) *ruleshiftv1.XiangqiDelta {
	payload, _ := xiangqi.DeltaPayload(delta)
	updates := make([]*ruleshiftv1.SquareUpdate, 0, len(payload.SquareUpdates))
	for _, update := range payload.SquareUpdates {
		updates = append(updates, &ruleshiftv1.SquareUpdate{
			Square: update.Square,
			Piece:  update.Piece,
		})
	}

	return &ruleshiftv1.XiangqiDelta{
		CommandType:           toProtoCommandType(delta.CommandType),
		MoveUci:               payload.MoveUCI,
		FromSquare:            payload.FromSquare,
		ToSquare:              payload.ToSquare,
		SquareUpdates:         updates,
		SideToMove:            toProtoSide(payload.SideToMove),
		Status:                toProtoStatus(delta.Status),
		WinnerPlayerId:        payload.WinnerPlayerID,
		DrawOfferedByPlayerId: payload.DrawOfferedByPlayerID,
		StateHash:             delta.StateHash,
	}
}

func toProtoGameType(gameType game.Type) ruleshiftv1.GameType {
	switch gameType {
	case game.TypeXiangqi:
		return ruleshiftv1.GameType_GAME_TYPE_XIANGQI
	default:
		return ruleshiftv1.GameType_GAME_TYPE_UNSPECIFIED
	}
}

func toProtoCommandType(commandType game.CommandType) ruleshiftv1.GameCommandType {
	switch commandType {
	case game.CommandDoMove:
		return ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_DO_MOVE
	case game.CommandResign:
		return ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_RESIGN
	case game.CommandOfferDraw:
		return ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_OFFER_DRAW
	default:
		return ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_UNSPECIFIED
	}
}

func toProtoSide(side xiangqi.Side) ruleshiftv1.XiangqiSide {
	switch side {
	case xiangqi.SideRed:
		return ruleshiftv1.XiangqiSide_XIANGQI_SIDE_RED
	case xiangqi.SideBlack:
		return ruleshiftv1.XiangqiSide_XIANGQI_SIDE_BLACK
	default:
		return ruleshiftv1.XiangqiSide_XIANGQI_SIDE_UNSPECIFIED
	}
}

func toProtoStatus(status game.Status) ruleshiftv1.GameStatus {
	switch status {
	case game.StatusActive:
		return ruleshiftv1.GameStatus_GAME_STATUS_ACTIVE
	case game.StatusResigned:
		return ruleshiftv1.GameStatus_GAME_STATUS_RESIGNED
	case game.StatusDrawOffered:
		return ruleshiftv1.GameStatus_GAME_STATUS_DRAW_OFFERED
	case game.StatusDrawn:
		return ruleshiftv1.GameStatus_GAME_STATUS_DRAWN
	default:
		return ruleshiftv1.GameStatus_GAME_STATUS_UNSPECIFIED
	}
}
