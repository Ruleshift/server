package main

import "testing"

func TestParseConsoleLine(t *testing.T) {
	cmd, args := parseConsoleLine("  ADD   10  ")
	if cmd != "add" {
		t.Fatalf("unexpected command: got=%q want=add", cmd)
	}
	if len(args) != 1 || args[0] != "10" {
		t.Fatalf("unexpected args: got=%v want=[10]", args)
	}
}

func TestParseOneInt64(t *testing.T) {
	value, err := parseOneInt64([]string{"-42"}, "add <value>")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if value != -42 {
		t.Fatalf("unexpected value: got=%d want=-42", value)
	}
}
