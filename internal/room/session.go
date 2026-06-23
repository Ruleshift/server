package room

import (
	"context"
	"errors"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/hiddennumber"
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

func SnapshotEnvelope(snapshot ProjectedStateSnapshot) *ruleshiftv1.ServerEnvelope {
	state := &ruleshiftv1.StateSnapshot{
		RoomId:   snapshot.RoomID,
		Revision: snapshot.Revision,
		GameType: toProtoGameType(snapshot.Game.Type),
		ViewHash: snapshot.Game.ViewHash,
	}
	switch snapshot.Game.Type {
	case game.TypeXiangqi:
		state.State = &ruleshiftv1.StateSnapshot_Xiangqi{Xiangqi: toProtoXiangqiSnapshot(snapshot.Game)}
	case game.TypeHiddenNumber:
		state.State = &ruleshiftv1.StateSnapshot_HiddenNumber{HiddenNumber: toProtoHiddenNumberSnapshot(snapshot.Game)}
	}
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateSnapshot{StateSnapshot: state}}
}

func DeltaEnvelope(delta ProjectedStateDelta) *ruleshiftv1.ServerEnvelope {
	stateDelta := &ruleshiftv1.StateDelta{
		RoomId:            delta.RoomID,
		PreviousRevision:  delta.PreviousRevision,
		NewRevision:       delta.NewRevision,
		ChangedByPlayerId: delta.ChangedByPlayerID,
		GameType:          toProtoGameType(delta.Game.Type),
		ViewHash:          delta.Game.ViewHash,
		NoVisibleChange:   delta.Game.NoVisibleChange,
	}
	switch delta.Game.Type {
	case game.TypeXiangqi:
		stateDelta.Delta = &ruleshiftv1.StateDelta_Xiangqi{Xiangqi: toProtoXiangqiDelta(delta.Game)}
	case game.TypeHiddenNumber:
		if !delta.Game.NoVisibleChange {
			stateDelta.Delta = &ruleshiftv1.StateDelta_HiddenNumber{HiddenNumber: toProtoHiddenNumberDelta(delta.Game)}
		}
	}
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateDelta{StateDelta: stateDelta}}
}

func toProtoXiangqiSnapshot(snapshot game.ViewSnapshot) *ruleshiftv1.XiangqiSnapshot {
	payload, _ := xiangqiViewSnapshotPayload(snapshot)
	return &ruleshiftv1.XiangqiSnapshot{
		Fen:                   payload.FEN,
		Board:                 payload.Board,
		SideToMove:            toProtoSide(payload.SideToMove),
		Status:                toProtoStatus(snapshot.Status),
		RedPlayerId:           payload.RedPlayerID,
		BlackPlayerId:         payload.BlackPlayerID,
		WinnerPlayerId:        payload.WinnerPlayerID,
		DrawOfferedByPlayerId: payload.DrawOfferedByPlayerID,
		StateHash:             snapshot.ViewHash,
	}
}

func toProtoXiangqiDelta(delta game.ViewDelta) *ruleshiftv1.XiangqiDelta {
	payload, _ := xiangqiViewDeltaPayload(delta)
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
		StateHash:             delta.ViewHash,
	}
}

func xiangqiViewSnapshotPayload(snapshot game.ViewSnapshot) (xiangqi.Snapshot, bool) {
	switch payload := snapshot.Payload.(type) {
	case xiangqi.Snapshot:
		return payload, true
	case *xiangqi.Snapshot:
		if payload != nil {
			return *payload, true
		}
	}
	return xiangqi.Snapshot{}, false
}

func xiangqiViewDeltaPayload(delta game.ViewDelta) (xiangqi.Delta, bool) {
	switch payload := delta.Payload.(type) {
	case xiangqi.Delta:
		return payload, true
	case *xiangqi.Delta:
		if payload != nil {
			return *payload, true
		}
	}
	return xiangqi.Delta{}, false
}

func toProtoHiddenNumberSnapshot(snapshot game.ViewSnapshot) *ruleshiftv1.HiddenNumberSnapshot {
	payload, _ := snapshot.Payload.(hiddennumber.SnapshotView)
	players := make([]*ruleshiftv1.HiddenNumberPlayerView, 0, len(payload.Players))
	for _, player := range payload.Players {
		view := &ruleshiftv1.HiddenNumberPlayerView{PlayerId: player.PlayerID, HasSecret: player.HasSecret}
		if player.Secret != nil {
			secret := *player.Secret
			view.Secret = &secret
		}
		players = append(players, view)
	}
	return &ruleshiftv1.HiddenNumberSnapshot{Players: players, Status: toProtoStatus(snapshot.Status)}
}

func toProtoHiddenNumberDelta(delta game.ViewDelta) *ruleshiftv1.HiddenNumberDelta {
	payload, _ := delta.Payload.(hiddennumber.DeltaView)
	view := &ruleshiftv1.HiddenNumberDelta{PlayerId: payload.PlayerID, HasSecret: payload.HasSecret}
	if payload.Secret != nil {
		secret := *payload.Secret
		view.Secret = &secret
	}
	return view
}

func toProtoGameType(gameType game.Type) ruleshiftv1.GameType {
	switch gameType {
	case game.TypeXiangqi:
		return ruleshiftv1.GameType_GAME_TYPE_XIANGQI
	case game.TypeHiddenNumber:
		return ruleshiftv1.GameType_GAME_TYPE_HIDDEN_NUMBER
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
	case game.CommandSetSecret:
		return ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_SET_SECRET
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
