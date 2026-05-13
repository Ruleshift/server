package protocol

import (
	"errors"
	"testing"
)

func TestValidateClientEnvelopeAcceptsAuthRequest(t *testing.T) {
	env := ClientEnvelope{
		ProtocolVersion: CurrentVersion,
		ClientSequence:  1,
		Payload: AuthRequest{
			Ticket: "mock:player-1",
		},
	}

	if err := ValidateClientEnvelope(env); err != nil {
		t.Fatalf("ValidateClientEnvelope returned error: %v", err)
	}
}

func TestValidateClientEnvelopeRejectsUnsupportedVersion(t *testing.T) {
	env := ClientEnvelope{
		ProtocolVersion: CurrentVersion + 1,
		ClientSequence:  1,
		Payload:         Ping{},
	}

	if err := ValidateClientEnvelope(env); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("error = %v, want ErrUnsupportedProtocolVersion", err)
	}
}

func TestValidateClientEnvelopeRejectsMissingPayload(t *testing.T) {
	env := ClientEnvelope{ProtocolVersion: CurrentVersion, ClientSequence: 1}

	if err := ValidateClientEnvelope(env); !errors.Is(err, ErrMissingPayload) {
		t.Fatalf("error = %v, want ErrMissingPayload", err)
	}
}

func TestValidateClientEnvelopeRejectsUnknownPayload(t *testing.T) {
	env := ClientEnvelope{
		ProtocolVersion: CurrentVersion,
		ClientSequence:  1,
		Payload:         UnknownClientPayload{FieldNumber: 99},
	}

	if err := ValidateClientEnvelope(env); !errors.Is(err, ErrUnknownPayload) {
		t.Fatalf("error = %v, want ErrUnknownPayload", err)
	}
}

func TestValidateClientEnvelopeRejectsInvalidIntCommand(t *testing.T) {
	env := ClientEnvelope{
		ProtocolVersion: CurrentVersion,
		ClientSequence:  1,
		Payload: IntCommand{
			RoomID:    "room-1",
			Operation: IntOperationUnspecified,
		},
	}

	if err := ValidateClientEnvelope(env); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("error = %v, want ErrInvalidEnvelope", err)
	}
}

func TestValidateServerEnvelopeRejectsNonIncreasingDeltaRevision(t *testing.T) {
	env := ServerEnvelope{
		ProtocolVersion: CurrentVersion,
		ServerSequence:  1,
		Payload: StateDelta{
			RoomID:            "room-1",
			PreviousRevision:  2,
			NewRevision:       2,
			ChangedByPlayerID: "player-1",
			Operation:         IntOperationAdd,
		},
	}

	if err := ValidateServerEnvelope(env); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("error = %v, want ErrInvalidEnvelope", err)
	}
}
