# Steam-Compatible Authentication

Ruleshift keeps authentication replaceable behind `internal/auth.Provider`:

```go
type Provider interface {
    AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error)
}
```

Provider output is a server-side identity:

```go
type Identity struct {
    PlayerID          string
    SteamID           string
    DisplayName       string
    AppID             string
    OwnershipVerified bool
}
```

Room logic only depends on `PlayerID`. Steam-specific fields stay in the gateway/auth boundary, so the future room and card-game logic can run with mock auth, Steam auth, or another identity provider.

## Local Mock Provider

`MockProvider` is for local development and tests. It accepts tickets shaped like:

```text
mock:player-1
```

The returned `PlayerID` is stable (`player-1` for the example above), `DisplayName` matches the player id, `AppID` is `local`, and `OwnershipVerified` is `true`.

## Steam Web API Provider

`SteamWebAPIProvider` is the production-oriented provider skeleton. It validates tickets with Steam Web API `ISteamUserAuth/AuthenticateUserTicket`.

Runtime selection:

```powershell
$env:RULESHIFT_AUTH_PROVIDER="steam"
$env:RULESHIFT_STEAM_APP_ID="480"
$env:RULESHIFT_STEAM_WEB_API_KEY="<server-only-secret>"
go run ./cmd/gateway
```

Local development remains:

```powershell
$env:RULESHIFT_AUTH_PROVIDER="mock"
go run ./cmd/gateway
```

Important constraints:

- The Steam Web API key is read only from server environment variable `RULESHIFT_STEAM_WEB_API_KEY`.
- Do not put the Web API key in Unity, client config, source control, logs, generated code, or protobuf messages.
- The HTTP dependency is behind `auth.HTTPDoer`, so tests use `httptest.Server` instead of real Steam calls.
- Successful Steam identities use `PlayerID = "steam:" + SteamID` and keep the raw `SteamID` separately.
- `AuthenticateUserTicket` does not provide a player persona name, so `DisplayName` currently falls back to the SteamID. A future profile provider can enrich this without changing room logic.

## Unity Flow

1. Unity obtains a Steam auth session ticket through Steamworks.NET or another Steamworks bridge.
2. Unity sends that ticket in `AuthRequest.ticket` inside the protobuf `ClientEnvelope`.
3. The Go gateway receives the binary protobuf WebSocket frame and calls `auth.Provider.AuthenticateTicket`.
4. In Steam mode, `SteamWebAPIProvider` calls Steam Web API from the server process using the server-only API key.
5. The gateway binds the returned `Identity.PlayerID` to the `PlayerSession`.
6. The authenticated session may join rooms and send commands.
7. Room runtime receives only the server-authenticated `PlayerID`, applies commands sequentially, and broadcasts ordered protobuf revisions.

## Boundary Rules

- Clients send auth tickets, never trusted player IDs.
- Gateway owns authentication and session binding.
- `internal/auth` owns provider implementations.
- `internal/room` owns authoritative state and command ordering, independent of Steam.
- Steam auth can be replaced with mock auth or another provider without changing room reducers or runtime behavior.

## Failure Behavior

- Missing, malformed, expired, or rejected tickets return `AuthFailed`.
- Steam transport failures are treated as provider availability errors and do not authenticate the session.
- Rooms are not created or joined until authentication succeeds.
