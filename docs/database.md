# Database architecture

Ruleshift uses one control PostgreSQL database plus an isolated database for
each developer/module pair.

## Control database

- `developers` and hashed `developer_api_keys`;
- `users` and provider identities;
- `modules` and the current active version;
- immutable `module_versions` with OCI and descriptor digests;
- bounded `module_validation_runs`;
- `room_routes` pinned to developer/module/version/image digest.

Registry usernames, passwords, and tokens are never stored here.

## Module database

- `rooms`: revision, lifecycle, seed, opaque protobuf state and SHA-256 digest;
- `room_events`: generic lifecycle/command event, protobuf input/delta bytes,
  revision range and resulting state digest;
- `room_snapshots`: opaque protobuf state every 100 revisions and on eviction;
- additive developer tables declared by version manifest.

There are no game enums, reducer-specific event types, JSON command payloads, or
in-process Go module migrations.

## Declarative migrations

Version manifests may add tables using `string`, `int64`, `float64`, `bool`,
`timestamp`, and `json` columns. Migration versions are positive and immutable.
Ruleshift compiles the declaration to SQL and verifies migration checksums.

Modules do not receive database credentials and cannot query these databases.
Only trusted backends use the bounded `CreateRow`/`ListRows` Developer API.

## Local PostgreSQL

```powershell
docker compose up -d postgres
```

The gateway itself also requires Kubernetes for external module Deployments.
This is a breaking pre-production schema; reset old local volumes first:

```powershell
docker compose down -v
```
