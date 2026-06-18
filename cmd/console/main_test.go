package main

import "testing"

func TestParseConsoleLine(t *testing.T) {
	cmd, args := parseConsoleLine("  MOVE   h2e2  ")
	if cmd != "move" {
		t.Fatalf("unexpected command: got=%q want=move", cmd)
	}
	if len(args) != 1 || args[0] != "h2e2" {
		t.Fatalf("unexpected args: got=%v want=[h2e2]", args)
	}
}

func TestParseMove(t *testing.T) {
	move, err := parseMove([]string{"H2E2"})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if move != "h2e2" {
		t.Fatalf("unexpected move: got=%q want=h2e2", move)
	}
}
