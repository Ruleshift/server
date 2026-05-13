package auth

import (
	"context"
	"errors"
	"testing"
)

func TestMockProviderAuthenticatesStablePlayerID(t *testing.T) {
	provider := NewMockProvider()

	identity, err := provider.AuthenticateTicket(context.Background(), "mock:player-1")
	if err != nil {
		t.Fatalf("AuthenticateTicket returned error: %v", err)
	}

	if identity.PlayerID != "player-1" {
		t.Fatalf("PlayerID = %q, want %q", identity.PlayerID, "player-1")
	}
	if !identity.OwnershipVerified {
		t.Fatal("OwnershipVerified = false, want true")
	}
}

func TestMockProviderRejectsInvalidTicket(t *testing.T) {
	provider := NewMockProvider()

	_, err := provider.AuthenticateTicket(context.Background(), "steam-ticket")
	if !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("error = %v, want ErrInvalidTicket", err)
	}
}
