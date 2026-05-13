package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const defaultSteamAuthenticateUserTicketURL = "https://api.steampowered.com/ISteamUserAuth/AuthenticateUserTicket/v1/"

type SteamWebAPIProvider struct {
	APIKey   string
	AppID    string
	Endpoint string
	Client   *http.Client
}

func NewSteamWebAPIProvider(apiKey, appID string, client *http.Client) *SteamWebAPIProvider {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &SteamWebAPIProvider{
		APIKey:   apiKey,
		AppID:    appID,
		Endpoint: defaultSteamAuthenticateUserTicketURL,
		Client:   client,
	}
}

func (p *SteamWebAPIProvider) AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("steam auth canceled: %w", ctx.Err())
	default:
	}

	if p.APIKey == "" || p.AppID == "" || ticket == "" {
		return nil, fmt.Errorf("%w: steam web api provider is not configured", ErrProviderUnavailable)
	}

	return nil, fmt.Errorf("%w: steam web api call is scheduled for phase 6", ErrProviderUnavailable)
}
