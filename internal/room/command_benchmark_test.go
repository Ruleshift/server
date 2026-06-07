package room

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkApplyIntCommandAdd(b *testing.B) {
	now := time.Unix(100, 0).UTC()
	state := NewState("room-1", now)
	cmd := IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationAdd,
		Value:     1,
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var err error
		state, _, err = ApplyIntCommand(state, cmd, now)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkApplyIntCommandConcurrentSubmit(b *testing.B) {
	runtime, err := NewRuntime("room-1", RuntimeConfig{InputQueueSize: 4096})
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
			if _, err := runtime.Submit(ctx, IntCommand{
				RoomID:    "room-1",
				PlayerID:  playerID,
				Operation: OperationAdd,
				Value:     1,
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

var benchmarkPlayerID atomic.Uint64
