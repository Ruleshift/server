package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/game/xiangqi"
	"github.com/Ruleshift/server/internal/gateway"
	"github.com/Ruleshift/server/internal/room"
)

func TestRunGetMove(t *testing.T) {
	addr, registry, cleanup := startTestGateway(t)
	defer cleanup()

	base := testOptions(addr)

	get := base
	get.op = "get"
	if err := run(context.Background(), get); err != nil {
		t.Fatalf("get failed: %v", err)
	}

	move := base
	move.op = "move"
	move.move = "h2e2"
	if err := run(context.Background(), move); err != nil {
		t.Fatalf("move failed: %v", err)
	}

	runtime, _, err := registry.GetOrCreate(base.roomID)
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := runtime.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	xSnapshot, ok := xiangqi.SnapshotPayload(snapshot.Game)
	if !ok {
		t.Fatalf("snapshot payload = %T, want xiangqi.Snapshot", snapshot.Game.Payload)
	}
	if xSnapshot.FEN == "" {
		t.Fatal("snapshot FEN is empty")
	}
	if snapshot.Game.StateHash == 0 {
		t.Fatal("snapshot state hash is zero after move")
	}
	if snapshot.Revision != 2 {
		t.Fatalf("unexpected final revision: got=%d want=2", snapshot.Revision)
	}
}

func startTestGateway(t *testing.T) (string, *room.Registry, func()) {
	t.Helper()

	registry := room.NewRegistry(room.RuntimeConfig{InputQueueSize: 16, GameModule: xiangqi.NewModule()})
	handler, err := gateway.New(gateway.Config{
		MaxMessageBytes:      defaultMaxMessageBytes,
		SessionSendQueueSize: 16,
		AuthTimeout:          time.Second,
	}, auth.NewMockProvider(), registry, slog.Default())
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(handler.HandleWebSocket))
	addr := "ws" + strings.TrimPrefix(server.URL, "http")

	cleanup := func() {
		server.Close()
		handler.Close()
	}
	return addr, registry, cleanup
}

func testOptions(addr string) options {
	return options{
		addr:             addr,
		ticket:           "mock:player-1",
		roomID:           "cli-room",
		op:               "get",
		timeout:          time.Second,
		maxMessageBytes:  defaultMaxMessageBytes,
		handshakeTimeout: time.Second,
	}
}
