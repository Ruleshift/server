package protocol

import (
	"errors"
	"testing"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

func TestValidateClientEnvelopeAcceptsAuthRequest(t *testing.T) {
	env := &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: CurrentVersion,
		ClientSequence:  1,
		Payload: &ruleshiftv1.ClientEnvelope_AuthRequest{
			AuthRequest: &ruleshiftv1.AuthRequest{Ticket: "mock:player-1"},
		},
	}

	if err := ValidateClientEnvelope(env); err != nil {
		t.Fatalf("ValidateClientEnvelope returned error: %v", err)
	}
}

func TestValidateClientEnvelopeRejectsUnsupportedVersion(t *testing.T) {
	env := &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: CurrentVersion + 1,
		ClientSequence:  1,
		Payload:         &ruleshiftv1.ClientEnvelope_Ping{Ping: &ruleshiftv1.Ping{}},
	}

	if err := ValidateClientEnvelope(env); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("error = %v, want ErrUnsupportedProtocolVersion", err)
	}
}

func TestValidateClientEnvelopeRejectsMissingPayload(t *testing.T) {
	env := &ruleshiftv1.ClientEnvelope{ProtocolVersion: CurrentVersion, ClientSequence: 1}

	if err := ValidateClientEnvelope(env); !errors.Is(err, ErrMissingPayload) {
		t.Fatalf("error = %v, want ErrMissingPayload", err)
	}
}

func TestValidateClientEnvelopeRejectsInvalidIntCommand(t *testing.T) {
	env := &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: CurrentVersion,
		ClientSequence:  1,
		Payload: &ruleshiftv1.ClientEnvelope_IntCommand{
			IntCommand: &ruleshiftv1.IntCommand{
				RoomId:    "room-1",
				Operation: ruleshiftv1.IntOperation_INT_OPERATION_UNSPECIFIED,
			},
		},
	}

	if err := ValidateClientEnvelope(env); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("error = %v, want ErrInvalidEnvelope", err)
	}
}

func TestValidateServerEnvelopeRejectsNonIncreasingDeltaRevision(t *testing.T) {
	env := &ruleshiftv1.ServerEnvelope{
		ProtocolVersion: CurrentVersion,
		ServerSequence:  1,
		Payload: &ruleshiftv1.ServerEnvelope_StateDelta{
			StateDelta: &ruleshiftv1.StateDelta{
				RoomId:            "room-1",
				PreviousRevision:  2,
				NewRevision:       2,
				ChangedByPlayerId: "player-1",
				Operation:         ruleshiftv1.IntOperation_INT_OPERATION_ADD,
			},
		},
	}

	if err := ValidateServerEnvelope(env); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("error = %v, want ErrInvalidEnvelope", err)
	}
}
