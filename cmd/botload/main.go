package main

import (
	"flag"
	"log/slog"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", "ws://localhost:8080/ws", "gateway WebSocket address")
	players := flag.Int("players", 100, "number of simulated players")
	rooms := flag.Int("rooms", 10, "number of target rooms")
	duration := flag.Duration("duration", 30*time.Second, "test duration")
	rps := flag.Int("rps", 100, "aggregate command rate")
	slowConsumerPercent := flag.Float64("slow-consumer-percent", 0, "percentage of clients that intentionally read slowly")
	reconnectPercent := flag.Float64("reconnect-percent", 0, "percentage of clients that reconnect during the run")
	seed := flag.Int64("seed", 1, "deterministic random seed")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info(
		"botload skeleton ready",
		"addr", *addr,
		"players", *players,
		"rooms", *rooms,
		"duration", duration.String(),
		"rps", *rps,
		"slow_consumer_percent", *slowConsumerPercent,
		"reconnect_percent", *reconnectPercent,
		"seed", *seed,
	)
}
