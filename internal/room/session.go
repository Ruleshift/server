package room

import (
	"context"
	"errors"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

const (
	CloseReasonSlowConsumer = "slow_consumer"
	CloseReasonShutdown     = "shutdown"
	CloseReasonReplaced     = "replaced"
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
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateSnapshot{StateSnapshot: &ruleshiftv1.StateSnapshot{
		RoomId:   snapshot.RoomID,
		Value:    snapshot.Value,
		Revision: snapshot.Revision,
	}}}
}

func DeltaEnvelope(delta StateDelta) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateDelta{StateDelta: &ruleshiftv1.StateDelta{
		RoomId:            delta.RoomID,
		PreviousValue:     delta.PreviousValue,
		NewValue:          delta.NewValue,
		PreviousRevision:  delta.PreviousRevision,
		NewRevision:       delta.NewRevision,
		ChangedByPlayerId: delta.ChangedByPlayerID,
		Operation:         FromOperation(delta.Operation),
		Operand:           delta.Operand,
	}}}
}

func FromOperation(operation Operation) ruleshiftv1.IntOperation {
	switch operation {
	case OperationAdd:
		return ruleshiftv1.IntOperation_INT_OPERATION_ADD
	case OperationSet:
		return ruleshiftv1.IntOperation_INT_OPERATION_SET
	default:
		return ruleshiftv1.IntOperation_INT_OPERATION_UNSPECIFIED
	}
}
