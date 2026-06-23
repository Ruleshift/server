package auth

import (
	"context"
	"errors"
	"testing"
)

type recordingIdentityStore struct {
	identity Identity
	err      error
}

func (s *recordingIdentityStore) SaveIdentity(_ context.Context, identity Identity) error {
	s.identity = identity
	return s.err
}

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

func TestPersistingProviderSavesAuthenticatedIdentity(t *testing.T) {
	store := &recordingIdentityStore{}
	provider := NewPersistingProvider(NewMockProvider(), store)

	identity, err := provider.AuthenticateTicket(context.Background(), "mock:player-1")
	if err != nil {
		t.Fatalf("AuthenticateTicket returned error: %v", err)
	}
	if store.identity.PlayerID != identity.PlayerID || store.identity.Provider != "mock" {
		t.Fatalf("saved identity = %#v, want mock player-1", store.identity)
	}
}
