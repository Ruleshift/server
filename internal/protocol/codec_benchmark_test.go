package protocol

import (
	"testing"

	ruleshiftv2 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv2"
	"google.golang.org/protobuf/types/known/anypb"
)

func BenchmarkClientEnvelopeCodec(b *testing.B) {
	env := &ruleshiftv2.ClientEnvelope{ProtocolVersion: CurrentVersion, ClientSequence: 1, Payload: &ruleshiftv2.ClientEnvelope_GameCommand{GameCommand: &ruleshiftv2.GameCommand{RoomId: "room-1", ExpectedRevision: 10, Command: &anypb.Any{TypeUrl: "type.googleapis.com/example.Move", Value: []byte{8, 1, 16, 2}}}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		payload, err := EncodeClientEnvelope(env)
		if err != nil {
			b.Fatal(err)
		}
		if _, err = DecodeClientEnvelope(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkServerEnvelopeCodec(b *testing.B) {
	env := &ruleshiftv2.ServerEnvelope{ProtocolVersion: CurrentVersion, ServerSequence: 1, Payload: &ruleshiftv2.ServerEnvelope_StateDelta{StateDelta: &ruleshiftv2.StateDelta{RoomId: "room-1", PreviousRevision: 10, NewRevision: 11, ChangedByPlayerId: "player-1", Module: &ruleshiftv2.ModuleRef{ModuleId: "xiangqi", Version: "1.0.0"}, ViewDigest: make([]byte, 32), Delta: &anypb.Any{TypeUrl: "type.googleapis.com/example.Delta", Value: []byte{8, 1}}}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		payload, err := EncodeServerEnvelope(env)
		if err != nil {
			b.Fatal(err)
		}
		if _, err = DecodeServerEnvelope(payload); err != nil {
			b.Fatal(err)
		}
	}
}
