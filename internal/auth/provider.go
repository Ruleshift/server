package auth

import (
	"context"
	"errors"
)

var (
	ErrInvalidTicket       = errors.New("invalid auth ticket")
	ErrProviderUnavailable = errors.New("auth provider unavailable")
)

type Identity struct {
	PlayerID          string
	SteamID           string
	DisplayName       string
	AppID             string
	OwnershipVerified bool
}

type Provider interface {
	AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error)
}
