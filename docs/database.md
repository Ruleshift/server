# Database Architecture

Ruleshift uses PostgreSQL with database-per-tenant-module isolation. The gateway still supports an in-memory mode when `RULESHIFT_DATABASE_URL` is empty, but configured deployments persist authenticated users, room membership, and the authoritative room event stream.

## Databases

The default control database (for example `ruleshift_default`) contains:

- `developers`: SaaS tenants;
- `modules`: registered game modules and their dedicated database names;
- `users`: server-side player records;
- `user_identities`: provider identities such as mock or Steam-compatible identities;
- `developer_api_keys`: hashed, revocable tenant API keys;
- `ruleshift_schema_migrations`: immutable migration history and checksums.

Every developer/module pair receives a database named
`<RULESHIFT_MODULE_DATABASE_PREFIX><developer_id>_<module_name>`, for example
`ruleshift_module_default_xiangqi`. It contains the platform-owned tables:

- `rooms`: current room revision and lifecycle projection;
- `room_players`: durable membership and disconnect metadata;
- `room_events`: ordered authoritative event log used for replay;
- `ruleshift_schema_migrations`: platform and module migration history.

The module's own migrations run after these base migrations. Databases deliberately store `player_id` without a cross-database foreign key; the canonical identity remains in the control database.

## Defining A Module Schema

External developers define schemas through the Ruleshift SDK and its bounded declarative schema API. They do not receive database credentials or submit arbitrary SQL. See [developer-api.md](developer-api.md).

Built-in server modules can additionally own internal migrations through Go. A durable built-in module implements the optional `game.DatabaseModule` interface:

```go
func (Module) DatabaseDefinition() game.DatabaseDefinition {
    return game.DatabaseDefinition{
        Name: "my_game",
        Migrations: []game.DatabaseMigration{
            {
                Version: 1,
                Name:    "create_matches",
                SQL: `CREATE TABLE matches (
                    room_id TEXT PRIMARY KEY REFERENCES rooms(id),
                    result TEXT
                )`,
            },
        },
    }
}
```

`Name` must match `[a-z][a-z0-9_]{0,47}`. Migration versions are positive and unique inside the module. Migrations are forward-only: after a migration is applied, changing its name or SQL fails startup because Ruleshift verifies its SHA-256 checksum.

If command payloads contain concrete Go types, the module should also implement `game.CommandPayloadCodec`. Xiangqi does this for `xiangqi.Move`, allowing database events to replay into the same reducer after a process restart.

At startup the gateway:

1. creates the configured control database when absent;
2. applies control migrations and upserts the configured developer;
3. creates the tenant/module database when absent;
4. applies base room migrations, then module migrations;
5. registers the module in the control database;
6. uses the module database as `room.EventStore`.

Database identifiers are validated and quoted before `CREATE DATABASE`. The PostgreSQL account used by `RULESHIFT_DATABASE_ADMIN_URL` therefore needs `CREATEDB`; use a dedicated provisioning credential in production rather than a general application role.

## Run Locally

The included Compose setup starts PostgreSQL and the gateway:

```powershell
docker compose up --build
```

Developers inspect schemas and bounded row pages through the API or SDK rather than through direct database tools. The local developer endpoint is `http://localhost:8080/v1/developer/`; Compose configures the development bearer key `ruleshift-dev-key-change-me`.

To run the gateway directly against the Compose PostgreSQL service:

```powershell
$env:RULESHIFT_DATABASE_URL="postgres://ruleshift:ruleshift-dev@localhost:5432/ruleshift_default?sslmode=disable"
$env:RULESHIFT_DATABASE_ADMIN_URL="postgres://ruleshift:ruleshift-dev@localhost:5432/postgres?sslmode=disable"
go run ./cmd/gateway
```

The database credentials and port exposure in `compose.yaml` are development-only. Do not publish raw PostgreSQL ports to SaaS customers; hosted users receive only the tenant-scoped developer API.
