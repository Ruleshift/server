# Ruleshift Developer API v2

Developer API is used by Unity Editor tooling, CI, or a trusted backend. Its
bearer key must never be included in a player build.

The complete contract is in `api/developer.openapi.yaml`.

## Local prerequisites

Start PostgreSQL:

```powershell
docker compose up -d postgres
```

Run the v2 gateway in Kubernetes, or run it from an environment that can reach
and resolve Kubernetes ClusterIP services. Outside a cluster, set:

```text
RULESHIFT_DATABASE_URL
RULESHIFT_DATABASE_ADMIN_URL
RULESHIFT_DEVELOPER_API_KEY
RULESHIFT_KUBECONFIG
```

## Go SDK

```go
client, err := ruleshift.NewClient(baseURL, developerKey, nil)
module, err := client.CreateRuntimeModule(ctx, "my_game", "My Game")
version, err := client.PublishModuleVersion(ctx, publishRequest)
status, err := client.GetValidationStatus(ctx, module.Key, version.Ref.Version)
room, err := client.CreateRoom(ctx, ruleshift.CreateRoomRequest{
    ModuleID: module.Key,
    PlayerCount: 2,
})
```

`CreateRoom` returns a six-character `invite_code` using `0-9A-Z` and an
`invite_deadline` exactly 24 hours after `created_at`. `PlayerCount` must be
between the module manifest's `min_players` and `max_players`; zero/omitted
defaults to `max_players`.

Optional declarative module tables are accessed with `CreateRow` and `ListRows`.
Ruleshift never returns PostgreSQL credentials or accepts arbitrary SQL.

## Unity and NuGet SDK

The Editor-only UPM package is `sdk/unity/com.ruleshift.developer`. The same
client is packaged for .NET from `sdk/dotnet/Ruleshift.Developer`.

Available v2 operations include:

- `CreateRuntimeModuleAsync`;
- `PublishModuleVersionAsync`;
- `GetModuleVersionAsync`;
- `GetValidationStatusAsync`;
- `CreateRoomAsync` and `GetRoomAsync`;
- `CreateRowAsync` and `ListRowsAsync`.

## Security boundary

- developer keys are stored as hashes in the control DB;
- registry credentials are stored only in tenant Kubernetes Secrets;
- validation logs are bounded and never contain registry tokens;
- every module/version/room lookup is scoped to the authenticated developer;
- player authentication remains separate on protobuf WebSocket protocol v2.
