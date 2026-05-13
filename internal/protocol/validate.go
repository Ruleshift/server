package protocol

import (
	"errors"
	"fmt"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

var (
	ErrUnsupportedProtocolVersion = errors.New("unsupported protocol version")
	ErrMissingPayload             = errors.New("missing envelope payload")
	ErrInvalidEnvelope            = errors.New("invalid envelope")
)

func ValidateClientEnvelope(env *ruleshiftv1.ClientEnvelope) error {
	if env == nil {
		return fmt.Errorf("%w: nil client envelope", ErrInvalidEnvelope)
	}
	if env.GetProtocolVersion() != CurrentVersion {
		return fmt.Errorf("%w: got=%d want=%d", ErrUnsupportedProtocolVersion, env.GetProtocolVersion(), CurrentVersion)
	}
	if env.GetPayload() == nil {
		return ErrMissingPayload
	}

	switch payload := env.GetPayload().(type) {
	case *ruleshiftv1.ClientEnvelope_AuthRequest:
		if payload.AuthRequest == nil || payload.AuthRequest.GetTicket() == "" {
			return fmt.Errorf("%w: auth_request.ticket is required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ClientEnvelope_JoinRoom:
		if payload.JoinRoom == nil || payload.JoinRoom.GetRoomId() == "" {
			return fmt.Errorf("%w: join_room.room_id is required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ClientEnvelope_IntCommand:
		if payload.IntCommand == nil || payload.IntCommand.GetRoomId() == "" {
			return fmt.Errorf("%w: int_command.room_id is required", ErrInvalidEnvelope)
		}
		if !validCommandOperation(payload.IntCommand.GetOperation()) {
			return fmt.Errorf("%w: int_command.operation is invalid", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ClientEnvelope_SnapshotRequest:
		if payload.SnapshotRequest == nil || payload.SnapshotRequest.GetRoomId() == "" {
			return fmt.Errorf("%w: snapshot_request.room_id is required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ClientEnvelope_Ping:
		if payload.Ping == nil {
			return ErrMissingPayload
		}
	default:
		return fmt.Errorf("%w: unknown client payload %T", ErrInvalidEnvelope, payload)
	}

	return nil
}

func ValidateServerEnvelope(env *ruleshiftv1.ServerEnvelope) error {
	if env == nil {
		return fmt.Errorf("%w: nil server envelope", ErrInvalidEnvelope)
	}
	if env.GetProtocolVersion() != CurrentVersion {
		return fmt.Errorf("%w: got=%d want=%d", ErrUnsupportedProtocolVersion, env.GetProtocolVersion(), CurrentVersion)
	}
	if env.GetPayload() == nil {
		return ErrMissingPayload
	}

	switch payload := env.GetPayload().(type) {
	case *ruleshiftv1.ServerEnvelope_AuthOk:
		if payload.AuthOk == nil || payload.AuthOk.GetPlayerId() == "" {
			return fmt.Errorf("%w: auth_ok.player_id is required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ServerEnvelope_AuthFailed:
		if payload.AuthFailed == nil || payload.AuthFailed.GetReason() == "" {
			return fmt.Errorf("%w: auth_failed.reason is required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ServerEnvelope_JoinRoomOk:
		if payload.JoinRoomOk == nil || payload.JoinRoomOk.GetRoomId() == "" {
			return fmt.Errorf("%w: join_room_ok.room_id is required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ServerEnvelope_StateSnapshot:
		if payload.StateSnapshot == nil || payload.StateSnapshot.GetRoomId() == "" {
			return fmt.Errorf("%w: state_snapshot.room_id is required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ServerEnvelope_StateDelta:
		if payload.StateDelta == nil || payload.StateDelta.GetRoomId() == "" {
			return fmt.Errorf("%w: state_delta.room_id is required", ErrInvalidEnvelope)
		}
		if payload.StateDelta.GetChangedByPlayerId() == "" {
			return fmt.Errorf("%w: state_delta.changed_by_player_id is required", ErrInvalidEnvelope)
		}
		if !validCommandOperation(payload.StateDelta.GetOperation()) {
			return fmt.Errorf("%w: state_delta.operation is invalid", ErrInvalidEnvelope)
		}
		if payload.StateDelta.GetNewRevision() <= payload.StateDelta.GetPreviousRevision() {
			return fmt.Errorf("%w: state_delta revision must increase", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ServerEnvelope_Error:
		if payload.Error == nil || payload.Error.GetCode() == "" || payload.Error.GetMessage() == "" {
			return fmt.Errorf("%w: error code and message are required", ErrInvalidEnvelope)
		}
	case *ruleshiftv1.ServerEnvelope_Pong:
		if payload.Pong == nil {
			return ErrMissingPayload
		}
	default:
		return fmt.Errorf("%w: unknown server payload %T", ErrInvalidEnvelope, payload)
	}

	return nil
}

func validCommandOperation(operation ruleshiftv1.IntOperation) bool {
	return operation == ruleshiftv1.IntOperation_INT_OPERATION_ADD || operation == ruleshiftv1.IntOperation_INT_OPERATION_SET
}
