package protocol

import (
	"testing"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

func BenchmarkClientEnvelopeCodec(b *testing.B) {
	env := &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: CurrentVersion,
		ClientSequence:  1,
		Payload: &ruleshiftv1.ClientEnvelope_GameCommand{
			GameCommand: &ruleshiftv1.GameCommand{
				RoomId:           "room-1",
				ExpectedRevision: 10,
				Command: &ruleshiftv1.GameCommand_DoMove{
					DoMove: &ruleshiftv1.DoMove{FromSquare: 1, ToSquare: 2, MoveUci: "a0a1"},
				},
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
				PreviousRevision:  10,
				NewRevision:       11,
				ChangedByPlayerId: "player-1",
				GameType:          ruleshiftv1.GameType_GAME_TYPE_XIANGQI,
				Delta: &ruleshiftv1.StateDelta_Xiangqi{
					Xiangqi: &ruleshiftv1.XiangqiDelta{
						CommandType: ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_DO_MOVE,
						MoveUci:     "a0a1",
						FromSquare:  1,
						ToSquare:    2,
						SquareUpdates: []*ruleshiftv1.SquareUpdate{
							{Square: 1, Piece: 0},
							{Square: 2, Piece: 1},
						},
						SideToMove: ruleshiftv1.XiangqiSide_XIANGQI_SIDE_BLACK,
						Status:     ruleshiftv1.GameStatus_GAME_STATUS_ACTIVE,
						StateHash:  123,
					},
				},
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

func BenchmarkHiddenNumberSnapshotCodec(b *testing.B) {
	secret := int64(424242)
	env := &ruleshiftv1.ServerEnvelope{
		ProtocolVersion: CurrentVersion,
		Payload: &ruleshiftv1.ServerEnvelope_StateSnapshot{StateSnapshot: &ruleshiftv1.StateSnapshot{
			RoomId: "hidden-room", Revision: 12, GameType: ruleshiftv1.GameType_GAME_TYPE_HIDDEN_NUMBER, ViewHash: 99,
			State: &ruleshiftv1.StateSnapshot_HiddenNumber{HiddenNumber: &ruleshiftv1.HiddenNumberSnapshot{
				Players: []*ruleshiftv1.HiddenNumberPlayerView{
					{PlayerId: "player-a", HasSecret: true, Secret: &secret},
					{PlayerId: "player-b", HasSecret: true},
				},
			}},
		}},
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
