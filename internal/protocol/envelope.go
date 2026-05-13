package protocol

import (
	"errors"
	"fmt"
)

var (
	ErrUnsupportedProtocolVersion = errors.New("unsupported protocol version")
	ErrMissingPayload             = errors.New("missing envelope payload")
	ErrUnknownPayload             = errors.New("unknown envelope payload")
	ErrInvalidEnvelope            = errors.New("invalid envelope")
)

type IntOperation uint8

const (
	IntOperationUnspecified IntOperation = iota
	IntOperationAdd
	IntOperationSet
)

func (op IntOperation) ValidCommandOperation() bool {
	return op == IntOperationAdd || op == IntOperationSet
}

type ClientPayload interface {
	clientPayload()
}

type ServerPayload interface {
	serverPayload()
}

type ClientEnvelope struct {
	ProtocolVersion uint32
	ClientSequence  uint64
	Payload         ClientPayload
}

type ServerEnvelope struct {
	ProtocolVersion uint32
	ServerSequence  uint64
	Payload         ServerPayload
}

type AuthRequest struct {
	Ticket string
}

type JoinRoomRequest struct {
	RoomID           string
	LastSeenRevision uint64
}

type IntCommand struct {
	RoomID           string
	Operation        IntOperation
	Value            int64
	ExpectedRevision uint64
}

type SnapshotRequest struct {
	RoomID           string
	LastSeenRevision uint64
}

type Ping struct {
	ClientTimeUnixMS int64
}

type UnknownClientPayload struct {
	FieldNumber int
}

type AuthOk struct {
	PlayerID    string
	DisplayName string
}

type AuthFailed struct {
	Reason string
}

type JoinRoomOk struct {
	RoomID          string
	CurrentRevision uint64
}

type StateSnapshot struct {
	RoomID   string
	Value    int64
	Revision uint64
}

type StateDelta struct {
	RoomID            string
	PreviousValue     int64
	NewValue          int64
	PreviousRevision  uint64
	NewRevision       uint64
	ChangedByPlayerID string
	Operation         IntOperation
	Operand           int64
}

type ErrorMessage struct {
	Code    string
	Message string
}

type Pong struct {
	ClientTimeUnixMS int64
	ServerTimeUnixMS int64
}

type UnknownServerPayload struct {
	FieldNumber int
}

func (AuthRequest) clientPayload()          {}
func (JoinRoomRequest) clientPayload()      {}
func (IntCommand) clientPayload()           {}
func (SnapshotRequest) clientPayload()      {}
func (Ping) clientPayload()                 {}
func (UnknownClientPayload) clientPayload() {}

func (AuthOk) serverPayload()               {}
func (AuthFailed) serverPayload()           {}
func (JoinRoomOk) serverPayload()           {}
func (StateSnapshot) serverPayload()        {}
func (StateDelta) serverPayload()           {}
func (ErrorMessage) serverPayload()         {}
func (Pong) serverPayload()                 {}
func (UnknownServerPayload) serverPayload() {}

func ValidateClientEnvelope(env ClientEnvelope) error {
	if env.ProtocolVersion != CurrentVersion {
		return fmt.Errorf("%w: got=%d want=%d", ErrUnsupportedProtocolVersion, env.ProtocolVersion, CurrentVersion)
	}
	if env.Payload == nil {
		return ErrMissingPayload
	}

	switch payload := env.Payload.(type) {
	case AuthRequest:
		return validateAuthRequest(payload)
	case *AuthRequest:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateAuthRequest(*payload)
	case JoinRoomRequest:
		return validateJoinRoomRequest(payload)
	case *JoinRoomRequest:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateJoinRoomRequest(*payload)
	case IntCommand:
		return validateIntCommand(payload)
	case *IntCommand:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateIntCommand(*payload)
	case SnapshotRequest:
		return validateSnapshotRequest(payload)
	case *SnapshotRequest:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateSnapshotRequest(*payload)
	case Ping:
		return nil
	case *Ping:
		if payload == nil {
			return ErrMissingPayload
		}
		return nil
	default:
		return fmt.Errorf("%w: %T", ErrUnknownPayload, env.Payload)
	}
}

func ValidateServerEnvelope(env ServerEnvelope) error {
	if env.ProtocolVersion != CurrentVersion {
		return fmt.Errorf("%w: got=%d want=%d", ErrUnsupportedProtocolVersion, env.ProtocolVersion, CurrentVersion)
	}
	if env.Payload == nil {
		return ErrMissingPayload
	}

	switch payload := env.Payload.(type) {
	case AuthOk:
		return validateAuthOk(payload)
	case *AuthOk:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateAuthOk(*payload)
	case AuthFailed:
		return validateAuthFailed(payload)
	case *AuthFailed:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateAuthFailed(*payload)
	case JoinRoomOk:
		return validateJoinRoomOk(payload)
	case *JoinRoomOk:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateJoinRoomOk(*payload)
	case StateSnapshot:
		return validateStateSnapshot(payload)
	case *StateSnapshot:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateStateSnapshot(*payload)
	case StateDelta:
		return validateStateDelta(payload)
	case *StateDelta:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateStateDelta(*payload)
	case ErrorMessage:
		return validateErrorMessage(payload)
	case *ErrorMessage:
		if payload == nil {
			return ErrMissingPayload
		}
		return validateErrorMessage(*payload)
	case Pong:
		return nil
	case *Pong:
		if payload == nil {
			return ErrMissingPayload
		}
		return nil
	default:
		return fmt.Errorf("%w: %T", ErrUnknownPayload, env.Payload)
	}
}

func validateAuthRequest(payload AuthRequest) error {
	if payload.Ticket == "" {
		return fmt.Errorf("%w: auth_request.ticket is required", ErrInvalidEnvelope)
	}
	return nil
}

func validateJoinRoomRequest(payload JoinRoomRequest) error {
	if payload.RoomID == "" {
		return fmt.Errorf("%w: join_room.room_id is required", ErrInvalidEnvelope)
	}
	return nil
}

func validateIntCommand(payload IntCommand) error {
	if payload.RoomID == "" {
		return fmt.Errorf("%w: int_command.room_id is required", ErrInvalidEnvelope)
	}
	if !payload.Operation.ValidCommandOperation() {
		return fmt.Errorf("%w: int_command.operation is invalid", ErrInvalidEnvelope)
	}
	return nil
}

func validateSnapshotRequest(payload SnapshotRequest) error {
	if payload.RoomID == "" {
		return fmt.Errorf("%w: snapshot_request.room_id is required", ErrInvalidEnvelope)
	}
	return nil
}

func validateAuthOk(payload AuthOk) error {
	if payload.PlayerID == "" {
		return fmt.Errorf("%w: auth_ok.player_id is required", ErrInvalidEnvelope)
	}
	return nil
}

func validateAuthFailed(payload AuthFailed) error {
	if payload.Reason == "" {
		return fmt.Errorf("%w: auth_failed.reason is required", ErrInvalidEnvelope)
	}
	return nil
}

func validateJoinRoomOk(payload JoinRoomOk) error {
	if payload.RoomID == "" {
		return fmt.Errorf("%w: join_room_ok.room_id is required", ErrInvalidEnvelope)
	}
	return nil
}

func validateStateSnapshot(payload StateSnapshot) error {
	if payload.RoomID == "" {
		return fmt.Errorf("%w: state_snapshot.room_id is required", ErrInvalidEnvelope)
	}
	return nil
}

func validateStateDelta(payload StateDelta) error {
	if payload.RoomID == "" {
		return fmt.Errorf("%w: state_delta.room_id is required", ErrInvalidEnvelope)
	}
	if payload.ChangedByPlayerID == "" {
		return fmt.Errorf("%w: state_delta.changed_by_player_id is required", ErrInvalidEnvelope)
	}
	if !payload.Operation.ValidCommandOperation() {
		return fmt.Errorf("%w: state_delta.operation is invalid", ErrInvalidEnvelope)
	}
	if payload.NewRevision <= payload.PreviousRevision {
		return fmt.Errorf("%w: state_delta revision must increase", ErrInvalidEnvelope)
	}
	return nil
}

func validateErrorMessage(payload ErrorMessage) error {
	if payload.Code == "" || payload.Message == "" {
		return fmt.Errorf("%w: error code and message are required", ErrInvalidEnvelope)
	}
	return nil
}
