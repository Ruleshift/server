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
	"github.com/Ruleshift/server/internal/gateway"
	"github.com/Ruleshift/server/internal/room"
)

func TestRunGetAddSet(t *testing.T) {
	addr, registry, cleanup := startTestGateway(t)
	defer cleanup()

	base := testOptions(addr)

	get := base
	get.op = "get"
	if err := run(context.Background(), get); err != nil {
		t.Fatalf("get failed: %v", err)
	}

	add := base
	add.op = "add"
	add.value = 5
	if err := run(context.Background(), add); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	set := base
	set.ticket = "mock:player-2"
	set.op = "set"
	set.value = 42
	if err := run(context.Background(), set); err != nil {
		t.Fatalf("set failed: %v", err)
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
	if snapshot.Value != 42 {
		t.Fatalf("unexpected final value: got=%d want=42", snapshot.Value)
	}
	if snapshot.Revision != 2 {
		t.Fatalf("unexpected final revision: got=%d want=2", snapshot.Revision)
	}
}

func startTestGateway(t *testing.T) (string, *room.Registry, func()) {
	t.Helper()

	registry := room.NewRegistry(room.RuntimeConfig{InputQueueSize: 16})
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
