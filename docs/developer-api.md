# Ruleshift As A Service

Game developers use Ruleshift through a tenant-scoped HTTP API and an SDK. They do not receive PostgreSQL credentials and do not need to manage a VPS. A local Compose deployment uses the same contract as a hosted Ruleshift deployment; only the base URL and developer API key change.

The API contract is [developer.openapi.yaml](../api/developer.openapi.yaml).

## Local Service

Start the service:

```powershell
docker compose up --build
```

Local endpoint and credential:

```text
Base URL: http://localhost:8080
Developer API key: ruleshift-dev-key-change-me
```

The development key is declared only in `compose.yaml`. Hosted deployments must issue a separate secret per developer tenant.

## Go Package

```go
client, err := ruleshift.NewClient(
    "http://localhost:8080",
    os.Getenv("RULESHIFT_DEVELOPER_API_KEY"),
    nil,
)

module, err := client.CreateModule(ctx, ruleshift.CreateModuleRequest{
    Key:         "my_game",
    DisplayName: "My Game",
    Schema: ruleshift.ModuleSchema{Tables: []ruleshift.TableDefinition{
        {
            Name: "profiles",
            Columns: []ruleshift.ColumnDefinition{
                {Name: "player_id", Type: ruleshift.ColumnTypeString, PrimaryKey: true},
                {Name: "rating", Type: ruleshift.ColumnTypeInt64},
            },
        },
    }},
})

row, err := client.CreateRow(ctx, module.Key, "profiles", map[string]any{
    "player_id": "player-1",
    "rating":    1200,
})

rows, err := client.ListRows(ctx, module.Key, "profiles", 100, 0)
```

The package lives at `github.com/Ruleshift/server/pkg/ruleshift` while the project remains a single Go module.

## Unity Package

The Editor-only UPM package is in `sdk/unity/com.ruleshift.developer`. Add it from disk in Unity Package Manager, or add this entry to the Unity project's `Packages/manifest.json`:

```json
{
  "dependencies": {
    "com.ruleshift.developer": "file:../server/sdk/unity/com.ruleshift.developer"
  }
}
```

Its `RuleshiftDeveloperClient` provides `CreateModuleAsync`, `ListModulesAsync`, `GetSchemaAsync`, `CreateRowAsync`, and `ListRowsAsync`. See the package README for a complete example.

The same C# client can be packed for non-Unity tooling from `sdk/dotnet/Ruleshift.Developer` with `dotnet pack -c Release`.

## Security Boundary

- The developer key authenticates editor, CI, and trusted backend operations.
- The developer package is Editor-only and excluded from Unity player builds.
- Players continue to authenticate using short-lived game/Steam-compatible tickets over the protobuf WebSocket protocol.
- The API accepts a bounded declarative schema, not arbitrary SQL.
- Row reads are paginated to at most 200 rows.
- Writes cannot target Ruleshift-owned room, event, or migration tables.

Developer keys are stored as SHA-256 hashes in the control database and resolve every request to a tenant. The configured local key bootstraps the default developer; additional hosted keys can be issued or revoked without exposing database credentials.
