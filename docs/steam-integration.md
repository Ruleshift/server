# Steam-Compatible Authentication

Ruleshift keeps authentication replaceable behind:

```go
type Provider interface {
    AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error)
}
```

## Local Development

`MockProvider` accepts tickets shaped like:

```text
mock:player-1
```

It returns a stable server-side `PlayerID`.

## Intended Steam Flow

1. Unity obtains a Steam auth session ticket through Steamworks.NET or another Steamworks bridge.
2. Unity sends the ticket in `AuthRequest`.
3. The Go gateway calls `SteamWebAPIProvider`.
4. The provider validates ownership and Steam identity with Steam Web API.
5. The gateway binds the verified identity to its protocol v2 WebSocket session.
6. `roomcore` receives only the server-authenticated player identity.

## Security Notes

- Never put Steam Web API keys in Unity.
- Read API keys only from environment or secret manager.
- Do not let clients choose `player_id`.
- Keep room logic independent from Steam-specific fields.


