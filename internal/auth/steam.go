package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultSteamAuthenticateUserTicketURL = "https://api.steampowered.com/ISteamUserAuth/AuthenticateUserTicket/v1/"

const SteamWebAPIKeyEnv = "RULESHIFT_STEAM_WEB_API_KEY"

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type SteamWebAPIProvider struct {
	apiKey   string
	appID    string
	endpoint string
	client   HTTPDoer
}

type SteamWebAPIConfig struct {
	APIKey   string
	AppID    string
	Endpoint string
	Client   HTTPDoer
}

func NewSteamWebAPIProvider(cfg SteamWebAPIConfig) (*SteamWebAPIProvider, error) {
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.AppID = strings.TrimSpace(cfg.AppID)
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: %s must be set", ErrProviderUnavailable, SteamWebAPIKeyEnv)
	}
	if cfg.AppID == "" {
		return nil, fmt.Errorf("%w: steam app id must be set", ErrProviderUnavailable)
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultSteamAuthenticateUserTicketURL
	}
	parsedEndpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid steam auth endpoint: %w", ErrProviderUnavailable, err)
	}
	if parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return nil, fmt.Errorf("%w: invalid steam auth endpoint %q", ErrProviderUnavailable, cfg.Endpoint)
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Second}
	}
	return &SteamWebAPIProvider{
		apiKey:   cfg.APIKey,
		appID:    cfg.AppID,
		endpoint: cfg.Endpoint,
		client:   cfg.Client,
	}, nil
}

func NewSteamWebAPIProviderFromEnv(appID string, client HTTPDoer) (*SteamWebAPIProvider, error) {
	return NewSteamWebAPIProvider(SteamWebAPIConfig{
		APIKey: os.Getenv(SteamWebAPIKeyEnv),
		AppID:  appID,
		Client: client,
	})
}

func (p *SteamWebAPIProvider) AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("steam auth canceled: %w", ctx.Err())
	default:
	}

	if p == nil || p.client == nil || p.apiKey == "" || p.appID == "" || p.endpoint == "" {
		return nil, fmt.Errorf("%w: steam web api provider is not configured", ErrProviderUnavailable)
	}
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return nil, fmt.Errorf("%w: empty steam auth ticket", ErrInvalidTicket)
	}

	requestURL, err := p.authenticateUserTicketURL(ticket)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build steam auth request: %w", ErrProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("steam auth canceled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%w: steam auth request failed: %w", ErrProviderUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: steam auth returned HTTP %d", ErrProviderUnavailable, resp.StatusCode)
	}

	var decoded steamAuthenticateUserTicketResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("%w: decode steam auth response: %w", ErrProviderUnavailable, err)
	}

	if decoded.Response.Error != nil {
		return nil, fmt.Errorf("%w: steam auth error %d: %s", ErrInvalidTicket, decoded.Response.Error.ErrorCode, decoded.Response.Error.ErrorDesc)
	}

	params := decoded.Response.Params
	if params.Result != "OK" {
		return nil, fmt.Errorf("%w: steam auth result %q", ErrInvalidTicket, params.Result)
	}
	if params.SteamID == "" {
		return nil, fmt.Errorf("%w: steam auth response missing steamid", ErrProviderUnavailable)
	}

	return &Identity{
		PlayerID:          "steam:" + params.SteamID,
		SteamID:           params.SteamID,
		DisplayName:       params.SteamID,
		AppID:             p.appID,
		OwnershipVerified: true,
	}, nil
}

func (p *SteamWebAPIProvider) authenticateUserTicketURL(ticket string) (string, error) {
	parsed, err := url.Parse(p.endpoint)
	if err != nil {
		return "", fmt.Errorf("%w: parse steam auth endpoint: %w", ErrProviderUnavailable, err)
	}

	query := parsed.Query()
	query.Set("key", p.apiKey)
	query.Set("appid", p.appID)
	query.Set("ticket", ticket)
	query.Set("format", "json")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type steamAuthenticateUserTicketResponse struct {
	Response steamAuthenticateUserTicketBody `json:"response"`
}

type steamAuthenticateUserTicketBody struct {
	Params steamAuthenticateUserTicketParams `json:"params"`
	Error  *steamAuthenticateUserTicketError `json:"error"`
}

type steamAuthenticateUserTicketParams struct {
	Result       string `json:"result"`
	SteamID      string `json:"steamid"`
	OwnerSteamID string `json:"ownersteamid"`
}

type steamAuthenticateUserTicketError struct {
	ErrorCode int    `json:"errorcode"`
	ErrorDesc string `json:"errordesc"`
}
