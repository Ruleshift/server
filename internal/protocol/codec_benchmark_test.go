package protocol

import (
	"testing"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

func BenchmarkClientEnvelopeCodec(b *testing.B) {
	env := &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: CurrentVersion,
		ClientSequence:  1,
		Payload: &ruleshiftv1.ClientEnvelope_IntCommand{
			IntCommand: &ruleshiftv1.IntCommand{
				RoomId:           "room-1",
				Operation:        ruleshiftv1.IntOperation_INT_OPERATION_ADD,
				Value:            5,
				ExpectedRevision: 10,
			},
		},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		payload, err := EncodeClientEnvelope(env)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := DecodeClientEnvelope(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkServerEnvelopeCodec(b *testing.B) {
	env := &ruleshiftv1.ServerEnvelope{
		ProtocolVersion: CurrentVersion,
		ServerSequence:  1,
		Payload: &ruleshiftv1.ServerEnvelope_StateDelta{
			StateDelta: &ruleshiftv1.StateDelta{
				RoomId:            "room-1",
				PreviousValue:     4,
				NewValue:          9,
				PreviousRevision:  10,
				NewRevision:       11,
				ChangedByPlayerId: "player-1",
				Operation:         ruleshiftv1.IntOperation_INT_OPERATION_ADD,
				Operand:           5,
			},
		},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		payload, err := EncodeServerEnvelope(env)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := DecodeServerEnvelope(payload); err != nil {
			b.Fatal(err)
		}
	}
}
