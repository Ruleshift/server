package auth

import (
	"context"
	"fmt"
	"strings"
)

type MockProvider struct{}

func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

func (p *MockProvider) AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("mock auth canceled: %w", ctx.Err())
	default:
	}

	const prefix = "mock:"
	if !strings.HasPrefix(ticket, prefix) {
		return nil, fmt.Errorf("%w: expected %q prefix", ErrInvalidTicket, prefix)
	}

	playerID := strings.TrimSpace(strings.TrimPrefix(ticket, prefix))
	var permissions Permissions
	const trustedPrefix = "trusted:"
	if strings.HasPrefix(playerID, trustedPrefix) {
		playerID = strings.TrimSpace(strings.TrimPrefix(playerID, trustedPrefix))
		permissions |= PermissionViewFullState
	}
	if playerID == "" {
		return nil, fmt.Errorf("%w: empty mock player id", ErrInvalidTicket)
	}

	return &Identity{
		PlayerID:          playerID,
		Provider:          "mock",
		DisplayName:       playerID,
		AppID:             "local",
		OwnershipVerified: true,
		Permissions:       permissions,
	}, nil
}
