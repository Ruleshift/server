package room

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/xiangqi"
)

func BenchmarkApplyGameCommandMove(b *testing.B) {
	now := time.Unix(100, 0).UTC()
	module := testGameModule{}
	gameState, err := module.NewState(now)
	if err != nil {
		b.Fatalf("NewState returned error: %v", err)
	}
	state := NewState("room-1", module.Type(), gameState, now)
	cmd := GameCommand{
		RoomID:   "room-1",
		PlayerID: "player-1",
		Type:     game.CommandDoMove,
		Payload:  xiangqi.Move{UCI: "a0a1"},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		state, _, err = ApplyGameCommand(module, state, cmd, now)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkApplyGameCommandConcurrentSubmit(b *testing.B) {
	runtime, err := NewRuntime("room-1", RuntimeConfig{InputQueueSize: 4096, GameModule: testGameModule{}})
	if err != nil {
		b.Fatalf("NewRuntime returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		playerID := "player-" + strconv.Itoa(int(benchmarkPlayerID.Add(1)))
		for pb.Next() {
			if _, err := runtime.Submit(ctx, GameCommand{
				RoomID:   "room-1",
				PlayerID: playerID,
				Type:     game.CommandDoMove,
				Payload:  xiangqi.Move{UCI: "a0a1"},
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

var benchmarkPlayerID atomic.Uint64
