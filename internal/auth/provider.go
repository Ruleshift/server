package auth

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInvalidTicket       = errors.New("invalid auth ticket")
	ErrProviderUnavailable = errors.New("auth provider unavailable")
)

type Identity struct {
	PlayerID          string
	Provider          string
	SteamID           string
	DisplayName       string
	AppID             string
	OwnershipVerified bool
	Permissions       Permissions
}

type Permissions uint32

const (
	PermissionViewFullState Permissions = 1 << iota
)

func (p Permissions) Has(permission Permissions) bool {
	return p&permission != 0
}

type Provider interface {
	AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error)
}

// IdentityStore is implemented by the platform database. Keeping it in auth
// avoids making authentication depend on PostgreSQL or any other storage engine.
type IdentityStore interface {
	SaveIdentity(ctx context.Context, identity Identity) error
}

type PersistingProvider struct {
	provider Provider
	store    IdentityStore
}

func NewPersistingProvider(provider Provider, store IdentityStore) *PersistingProvider {
	return &PersistingProvider{provider: provider, store: store}
}

func (p *PersistingProvider) AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error) {
	identity, err := p.provider.AuthenticateTicket(ctx, ticket)
	if err != nil {
		return nil, err
	}
	if err := p.store.SaveIdentity(ctx, *identity); err != nil {
		return nil, fmt.Errorf("persist authenticated identity: %w", err)
	}
	return identity, nil
}
