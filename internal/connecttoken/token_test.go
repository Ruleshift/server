package connecttoken

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestManagerGenerateAndValidate(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	manager := newTestManager(t, now)

	token, err := manager.Generate(Claims{
		AssignmentID: "assignment-1",
		MatchID:      "match-1",
		ServerID:     "server-1",
		PlayerID:     "player-1",
		ExpiresAt:    now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	claims, err := manager.Validate(token)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if claims.AssignmentID != "assignment-1" || claims.MatchID != "match-1" || claims.ServerID != "server-1" || claims.PlayerID != "player-1" {
		t.Fatalf("claims = %#v, want generated values", claims)
	}
}

func TestManagerRejectsTamperedToken(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	manager := newTestManager(t, now)

	token, err := manager.Generate(Claims{
		AssignmentID: "assignment-1",
		MatchID:      "match-1",
		ServerID:     "server-1",
		PlayerID:     "player-1",
		ExpiresAt:    now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	tampered := token[:len(token)-1] + "A"
	if _, err := manager.Validate(tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Validate error = %v, want ErrInvalidToken", err)
	}
}

func TestManagerRejectsExpiredToken(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	current := now
	manager, err := NewManagerWithClock([]byte(strings.Repeat("s", 32)), func() time.Time {
		return current
	})
	if err != nil {
		t.Fatalf("NewManagerWithClock returned error: %v", err)
	}

	token, err := manager.Generate(Claims{
		AssignmentID: "assignment-1",
		MatchID:      "match-1",
		ServerID:     "server-1",
		PlayerID:     "player-1",
		ExpiresAt:    now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	current = now.Add(2 * time.Second)
	if _, err := manager.Validate(token); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("Validate error = %v, want ErrExpiredToken", err)
	}
}

func newTestManager(t *testing.T, now time.Time) *Manager {
	t.Helper()

	manager, err := NewManagerWithClock([]byte(strings.Repeat("s", 32)), func() time.Time {
		return now
	})
	if err != nil {
		t.Fatalf("NewManagerWithClock returned error: %v", err)
	}
	return manager
}
