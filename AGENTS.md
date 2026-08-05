# AGENTS.md

This file is the repository-wide operating guide for coding agents. Read it
before changing code. A more deeply nested `AGENTS.md` overrides this file for
its subtree; in particular, `site/AGENTS.md` contains the website workflow and
visual QA rules.

## Project in one paragraph

Ruleshift is a Go authoritative multiplayer state service for game developers.
Trusted backends publish immutable game-module versions and create rooms.
Player clients authenticate over a binary protobuf WebSocket, join a room, and
send intent-only protobuf commands. Ruleshift owns the canonical room state,
orders every mutation through one bounded sequential room runtime, invokes the
room's pinned stateless gRPC/OCI game module, persists generic events and
snapshots in PostgreSQL, and sends each recipient a private/public/full
projection of the same revision stream.

The production core is deliberately game-agnostic. Game state, commands, rules,
and projections live in separately built OCI images. Adding a game must not
require rebuilding Ruleshift.

## Source-of-truth order

When sources disagree, use this order:

1. Compiling implementation and tests.
2. Protobuf and OpenAPI contracts.
3. This file.
4. English and Russian documentation.
5. Historical wording in README or performance plans.

Do not preserve an obsolete statement just because it appears in a document.
Update affected documentation when behavior, architecture, protocol, commands,
configuration, or deployment changes.

## Current status: implemented versus not wired

Implemented and wired by `cmd/gateway`:

- PostgreSQL-backed control plane and per-developer/module databases;
- Developer API v2 with bearer-key tenant isolation;
- immutable OCI module publication, Kubernetes deployment, validation,
  conformance checking, activation, degradation, and version pinning;
- protobuf WebSocket player gateway at `/v2/ws`;
- authoritative opaque room state, revision ordering, events, snapshots, replay,
  and recipient-specific projections;
- 6-character `0-9A-Z` room invite codes with a 24-hour deadline;
- private Prometheus metrics and payload-free room diagnostics;
- a separate public read-only observability API;
- Go, Unity Editor, and .NET Developer API clients;
- independently built Hidden Number, Xiangqi, and Card Game example modules;
- k3s deployment assets and GitHub Actions image publication/deployment.

Implemented as a separate `cmd/gamejam-promotions` service:

- bounded HTTPS discovery adapters for GameDev Afisha, Jammer, and itch.io;
- manual Basic-authenticated moderation of Russian/Russian-language game jams;
- a dedicated PostgreSQL database with encrypted shared 10-digit promotion codes;
- public date-bounded code verification used by the static website;
- separate public/admin listeners, metrics, OpenAPI, tests, and k3s assets.

This service is not part of the authoritative gateway, Developer API, control
database, module databases, room protocol, or player identity system.

Present as tested in-memory library code, but not wired into a command, database,
HTTP API, player protocol, or production deployment:

- `internal/allocator`;
- `internal/matchmaking`;
- `internal/connecttoken`.

Not complete:

- `SteamWebAPIProvider.AuthenticateTicket` is a stub, and the gateway currently
  wires `MockProvider`;
- `cmd/botload` only parses flags and logs its configuration; it does not open
  WebSockets or generate load;
- the performance report is a target/template, not measured network results;
- player SDK packaging is not complete; the checked-in Unity and .NET packages
  are trusted Developer API clients, not full runtime player SDKs;
- the static `site/` currently contains product/docs UI and no live
  observability fetch in `App.jsx`;
- invite codes are stored and returned, but there is no join-by-code endpoint;
- `/healthz` and `/readyz` are process-only constant responses, not dependency
  health checks;
- `RULESHIFT_ENABLE_PPROF` is parsed but no pprof routes are registered.

The old “shared `int64` reducer” description is historical. Do not reintroduce
an in-process integer reducer, legacy v1 packages, FlatBuffers, or game-specific
oneofs into the core.

## Architecture and request flow

```text
trusted backend / Editor / CI
        |
        | HTTP + Developer bearer key
        v
Developer API v2 -> control DB -> validator -> Kubernetes module workload
        |
        | creates an immutable room route
        v
player -> protobuf v2 WebSocket -> gateway session/hub
        -> room registry -> bounded sequential room runtime
        -> pinned Module Runtime gRPC service
        -> module DB transaction (state + event + optional snapshot)
        -> per-viewer projected snapshot/delta -> bounded session queue
```

The separate monitoring path is:

```text
gateway private /metrics -> Prometheus
gateway JSON logs        -> Alloy -> Loki
observability-api        -> fixed PromQL + gateway private operations API
Grafana                  -> private Prometheus + Loki
```

## Non-negotiable design rules

- The server is authoritative. Clients send commands/intents, never canonical
  state, revisions, player identity, server time, or trusted randomness.
- Protobuf is the client/server and module wire format. Do not introduce JSON
  player commands, FlatBuffers, or game-specific core protocol fields.
- Keep the core game-agnostic. Production code must not import
  `examples/modules` or an `internal/game` implementation. Architecture tests
  enforce this.
- A room is permanently pinned to
  `developer_id + module_id + version + image_digest`. Never silently upgrade
  an existing room.
- A module is stateless and side-effect free. It receives current state and
  authenticated context and returns next state/delta/projections. It must not
  keep a `room_id -> state` map or use a database, writable filesystem, outside
  network, wall clock, process randomness, or client-provided identity.
- Keep all queues bounded and all long-running operations cancellable through
  `context.Context`.
- Never perform player network writes from the authoritative room loop.
  Module RPC and persistence are part of the current serialized transition;
  WebSocket writes belong to session writer goroutines.
- Preserve explicit limits for payloads, request bodies, queues, logs,
  diagnostics, result pages, and retries. Do not replace them with unbounded
  reads, goroutines, maps, channels, or fan-out.
- Expected module rejections and temporary unavailability must not increment a
  room revision. Only a committed changed transition increments it.
- Never expose canonical state, private projections, internal room IDs, player
  IDs, developer IDs, secrets, credentials, database URLs, or protobuf payloads
  through observability.
- Prefer small, reviewable changes. Preserve unrelated user work in a dirty
  worktree.

## Repository map and ownership

| Path | Responsibility |
| --- | --- |
| `cmd/gateway` | Composition root for PostgreSQL, Kubernetes, room registry, auth, Developer API, public HTTP/WebSocket, and private operations listener. Keep domain logic out. |
| `cmd/observability-api` | Public read-only aggregate/room observability process. |
| `cmd/gamejam-promotions` | Standalone game-jam discovery, moderation, and promotion-code process with separate public/admin listeners and database. |
| `cmd/botload` | Future load-generator CLI; currently a skeleton. |
| `internal/auth` | Replaceable identity providers and identity persistence boundary. |
| `internal/protocol` | Player protobuf v2 schema, generated Go bindings, framing codec, and codec benchmarks. |
| `internal/gatewayv2` | WebSocket transport, authentication sequencing, room membership hubs, bounded outbound queues, and envelope conversion. |
| `internal/module` | Language-neutral opaque state model, hard payload/deadline limits, runtime interface, and gRPC error classification. |
| `internal/moduleruntime` | Module Runtime ABI v2 schema and generated Go/gRPC bindings. |
| `internal/runtimeclient` | Token-authenticated gRPC connections to module workloads and endpoint resolution. |
| `internal/roomcore` | Authoritative room state, sequential runtime, revision stream, event replay, snapshots, registry, and payload-free diagnostics. It must remain independent of HTTP, WebSocket, Steam, and game implementations. |
| `internal/controlplane` | Module manifests, publication validation, conformance, lifecycle, active-version selection, and protocol-violation tracking. |
| `internal/developerapi` | Tenant-authenticated HTTP v2 handlers and room provisioning. |
| `internal/storage/postgres` | Embedded migrations, control/module database provisioning, stores, additive schema compiler, safe row API, and identity/API-key persistence. |
| `internal/scheduler/kubernetes` | Tenant namespaces, isolation policies, Secrets, module Deployments/Services, readiness diagnostics, and cleanup. |
| `internal/metrics` | Bounded-label Prometheus instrumentation. |
| `internal/operations` | Private payload-free room diagnostics and opaque public room references. |
| `internal/observabilityapi` | Fixed PromQL overview and bounded proxy to private room diagnostics. |
| `internal/gamejampromo` | Bounded external-source discovery, manual moderation, encrypted promotion codes, dedicated PostgreSQL store, public verification, and Basic-authenticated admin UI. It must not import gateway or room domain packages. |
| `internal/allocator` | In-memory game-server capacity/reservation library; not production-wired. |
| `internal/matchmaking` | In-memory ticket/match/assignment state machine; not production-wired. |
| `internal/connecttoken` | HMAC-signed assignment connect tokens; not production-wired. |
| `pkg/ruleshift` | Public Go Developer API v2 client. Avoid importing `internal/*` here. |
| `sdk/unity/com.ruleshift.developer` | Editor-only UPM Developer API client. Never include its bearer key in player builds. |
| `sdk/dotnet/Ruleshift.Developer` | netstandard2.1 package reusing the Unity client source. |
| `examples/modules` | Independent example OCI services and shared example host. They may contain game mechanics; production core may not import them. |
| `api` | Developer API and observability OpenAPI 3.1 contracts. |
| `docs`, `docs/ru` | English/Russian architecture and integration docs. Keep paired documents aligned when applicable. |
| `deploy` | Local observability Compose and production k3s/Kustomize assets. |
| `site` | Vite/React GitHub Pages site; obey `site/AGENTS.md`. |
| `scripts` | Protobuf generation, example module publication, and safe gateway rollout/rollback. |

## Player WebSocket protocol v2

Contract: `internal/protocol/proto/ruleshift.proto`.

- Endpoint: `/v2/ws`.
- Every WebSocket message must be binary and contain exactly one serialized
  `ClientEnvelope` or `ServerEnvelope`. There is no extra length prefix.
- Every client envelope must set `protocol_version = 2`. v1 is intentionally
  unsupported.
- `client_sequence` must be nonzero and strictly increasing per connection.
  The writer assigns an increasing `server_sequence`.
- `AuthRequest` must be first. The current local ticket forms are
  `mock:<player-id>` and `mock:trusted:<player-id>`; the latter grants full
  spectator view permission.
- A trusted backend creates the room before a player sends `JoinRoomRequest`.
- Explicit spectator mode gets public scope, or full scope for an authenticated
  identity with `PermissionViewFullState`. Unspecified/non-spectator mode is
  treated as player scope.
- Core owns player seats and sends recipient-specific snapshots to all
  connected members after roster/status changes. Join/leave never invoke the
  game module.
- `GameCommand.command` is a module-defined `google.protobuf.Any`.
  `expected_revision = 0` disables optimistic revision checking; any nonzero
  value must equal the current revision.
- Successful changed commands broadcast recipient-specific `StateDelta`
  values in committed revision order. `view_digest` is SHA-256 of the exact
  projected protobuf payload.
- `SnapshotRequest` produces a fresh recipient-specific snapshot.
- The current implementation does not use either `last_seen_revision` field
  for delta catch-up; recovery is snapshot-based.
- Stable gateway error codes include `bad_sequence`, `auth_required`,
  `bad_request`, `room_not_found`, `module_unavailable`, and
  `command_rejected`. Revision mismatch currently falls through to
  `bad_request`.
- The WebSocket origin check currently accepts every origin. Do not describe it
  as an origin allowlist until that is implemented.

Default full-frame limit is 64 KiB
(`RULESHIFT_MAX_MESSAGE_BYTES`) for both incoming and outgoing WebSocket
envelopes. This can be lower than the module ABI's 256 KiB message/view limit;
account for envelope overhead and the configured gateway limit.

### Slow consumers

Each session has a bounded outbound channel (default 256). Enqueue is
nonblocking:

- a new snapshot first drains queued messages, making it a recovery/compaction
  boundary;
- a full queue rejects a delta/snapshot enqueue and increments the slow-consumer
  metric;
- current code does not automatically disconnect slow consumers.

Do not change this into a blocking network path inside room processing.

## Module Runtime ABI v2

Contract: `internal/moduleruntime/proto/module_runtime.proto`. Module services
listen on gRPC port 50051 and implement:

- `Describe`;
- `CreateState`;
- `Apply`;
- `ProjectSnapshot`;
- `ProjectDelta`.

Hard limits from `internal/module`:

| Item | Limit |
| --- | ---: |
| canonical state | 1 MiB |
| command | 256 KiB |
| delta | 256 KiB |
| projected view | 256 KiB |
| default transition deadline | 50 ms |
| maximum transition deadline | 250 ms |
| `CreateState` deadline | 250 ms |
| retry | once, only for gRPC `Unavailable`, within the original deadline |

ABI rules:

- `DeterministicContext` supplies only current revision, server time, and the
  deterministic room seed. Module ABI requests do not expose room IDs,
  operation IDs, connection state, or lobby lifecycle.
- `CreateState` receives immutable `GameSetup.player_count`.
- Ruleshift Core persistently maps authenticated `player_id` values to
  zero-based seats. `Apply` receives an authenticated
  `Actor { player_id, seat_index }`; projections receive a resolved `Viewer`
  with optional seat and player/public/full scope.
- Module rules must derive randomness from the seed plus deterministic state
  such as revision.
- `CreateState` must return `changed=true`.
- Even `changed=false` transitions must return valid, correctly typed,
  in-limit `next_state` and `delta`; the gRPC client validates them before
  room logic inspects `changed`.
- A projection always needs a valid payload, including when
  `no_visible_change=true`.
- Canonical state never goes directly to a player. Projection code must check
  both viewer scope and resolved seat before exposing private information.
- gRPC `InvalidArgument`, `FailedPrecondition`, and `PermissionDenied` map
  to `ErrCommandRejected`; `Unavailable` and `DeadlineExceeded` map to
  `ErrUnavailable`; other RPC errors and malformed/oversized/wrong-type
  responses map to `ErrProtocolViolation`.
- Module calls use a random per-deployment bearer token stored in a Kubernetes
  Secret. Player tickets and Developer API keys are never forwarded.
- The Kubernetes gRPC health probe cannot send the bearer token, so health RPCs
  must be available without it while module RPCs enforce it.

## Authoritative room semantics

- `roomcore.Runtime` owns the only mutable in-memory copy of a room's canonical
  state.
- Each runtime has one goroutine and a bounded input channel (default 1024).
  Operations are serialized. Current submission behavior applies backpressure
  until queue space is available or the caller context ends; despite the
  exported `ErrQueueFull`, it does not currently fail fast on a full queue.
- Room creation calls module `CreateState`, writes revision 0 state, a
  `room_created` event, and a revision 0 snapshot.
- The module process stores no current match data. Core owns the canonical
  room state, lobby/active status, and persistent player-to-seat roster.
- A room is `lobby` until its immutable `player_count` seats are filled.
  Commands are rejected before `active`. A lobby disconnect frees the seat;
  after `active`, disconnect retains it for the same player's reconnect.
- Join/leave roster changes never call the module and never increment the game
  revision. A changed command increments revision by exactly one;
  `changed=false` commits nothing.
- For commands, nonzero `expected_revision` is checked before calling the
  module.
- Persistence happens before the in-memory state is replaced. A command event,
  resulting state/revision, and optional periodic snapshot are one module-DB
  transaction.
- Periodic snapshots are written every 100 revisions. Runtime shutdown also
  attempts a snapshot with a 5-second background timeout.
- Restore loads the pinned route, latest snapshot, and later generic events,
  replays each event through the exact pinned module, checks revision
  continuity, and verifies the resulting state digest.
- Registry entries are lazy-loaded and retained until registry shutdown; there
  is currently no active room eviction policy.
- Client network sends occur after room operations and use bounded session
  queues.

Important implementation caveat: current `Apply` code commits the changed
transition before running recipient projections. A later projection
error can therefore be returned after state/revision are already committed.
Do not claim that projections are part of the database transaction, and do not
assume such an error rolled the command back. If this ordering changes, add
failure-path tests and update architecture docs.

Room route creation spans two databases. The route and invite code are atomic
inside the control DB, then the initial room is created in its module DB. On a
module-DB failure, code makes a best-effort compensating delete of the control
route; this is not a distributed transaction.

## Module publication and lifecycle

The Developer API accepts only:

- a SemVer manifest with ABI version 2 and
  `1 <= min_players <= max_players <= 64`;
- an OCI reference containing `@sha256:<64 lowercase hex>`, never a tag-only
  reference;
- a nonempty protobuf descriptor set up to 4 MiB;
- required conformance vectors;
- module state/command type URLs present in the descriptor and outside reserved
  `ruleshift.*` packages;
- an optional declarative additive database schema.

Publication flow:

1. Persist immutable version as `validating`.
2. Ensure tenant Kubernetes isolation resources.
3. Create the version's token Secret, Deployment, and ClusterIP Service.
4. Wait for at least one updated ready replica. MVP intentionally does not set
   or enforce an explicit replica count.
5. Call `Describe` and compare identity, ABI, state type, command types,
   descriptor digest, and capabilities with the manifest.
6. Run conformance vectors twice and compare the full serialized outputs.
7. Apply additive database migrations.
8. Mark the new version `active` and the previous active version `inactive`.

The HTTP publish handler performs this validation synchronously. On success its
`201` response is already active. Publishing the same SemVer and image digest
is idempotent; the same SemVer with another digest is a conflict. Failed
validation marks the version `failed` and attempts bounded Kubernetes cleanup.

Room creation without an explicit version uses the active healthy version. If
that active version is no longer usable, it falls back to the most recently
updated inactive version. An explicit version must be active or inactive.
Existing pinned rooms continue using their exact image digest.

The in-process protocol-violation tracker marks a version `degraded` after
three protocol violations in a rolling 60-second window. Ordinary command
rejections and temporary unavailability do not count. The rolling counters
reset on process restart, while the persisted degraded status remains.

Conformance vectors must include `player_count`, initial-state digest, at least
one command with authenticated player/seat context, command/state/delta
digests, a public delta projection, and both player-private and public snapshot
projections.

## Developer API v2

Contract: `api/developer.openapi.yaml`. All routes require
`Authorization: Bearer <developer-key>`; keys belong only in Editor, CI, or a
trusted backend.

Routes:

```text
PUT  /v2/developer/registry-credentials/{name}
POST /v2/developer/modules
POST /v2/developer/modules/{module}/versions
GET  /v2/developer/modules/{module}/versions/{version}
GET  /v2/developer/modules/{module}/versions/{version}/validation
GET  /v2/developer/modules/{module}/tables/{table}/rows
POST /v2/developer/modules/{module}/tables/{table}/rows
POST /v2/rooms
GET  /v2/rooms/{room_id}
```

JSON request bodies are limited to 1 MiB, reject unknown fields, and must contain
exactly one JSON value. Publication multipart input is bounded; descriptor sets
have the 4 MiB limit and other individual parts have 1 MiB limits.

All lookups are scoped to the authenticated developer. Developer API keys are
stored as SHA-256 hashes and the bootstrap key must be at least 16 characters.
Registry credentials are written only as tenant Kubernetes
`dockerconfigjson` Secrets; they are not returned and are not stored in
PostgreSQL.

The safe row API does not accept SQL or credentials. It can access only module
tables, rejects Ruleshift internal tables, allows bounded inserts, defaults list
pages to 100, caps them at 200 rows, and caps offset at 1,000,000. Current list
queries have no `ORDER BY`; do not promise stable pagination order.

Room creation accepts optional `player_count`, defaulting to manifest
`max_players`, and validates it against the manifest range. It returns a random
32-hex-character room ID, random `uint64` seed, immutable module route,
six-character invite code, and deadline exactly 24 hours after creation.
Invite-code uniqueness retries at most eight times.

## PostgreSQL model and migration rules

Ruleshift uses:

- one control database for developers, hashed API keys, users/identities,
  modules, immutable versions, validation logs, pinned room routes, and invite
  codes;
- one database per `developer + module` for authoritative rooms, events,
  snapshots, and declared developer tables.

`RULESHIFT_DATABASE_URL` selects the control database. On startup the platform
uses `RULESHIFT_DATABASE_ADMIN_URL`, or derives a connection to the
`postgres` database, to create missing control/module databases. The database
role therefore needs database-creation privileges.

Embedded base migrations live under
`internal/storage/postgres/migrations/{control,module}`. They run under a
transaction-scoped PostgreSQL advisory lock and record a SHA-256 checksum in
`ruleshift_schema_migrations`. Never edit an applied migration; add the next
numbered file. Tests validate ordering and duplicate versions.

Manifest migrations are additive only:

- versions are positive and strictly increasing;
- names use safe lowercase identifiers;
- only new tables and the columns declared for those tables are accepted;
- supported types are `string`, `int64`, `float64`, `bool`,
  `timestamp`, and `json`;
- no raw SQL, `DROP`, or `ALTER`;
- applied declaration checksums are immutable.

State, input, delta, and snapshots are stored as protobuf type URL + opaque bytes
with SHA-256 state digests. Do not add game enums, reducer-specific event tables,
or JSON command columns to the production schema.

## Authentication

`internal/auth.Provider` is the abstraction. `roomcore` receives only an
already authenticated server-side player ID.

Current gateway wiring is:

```text
MockProvider -> PersistingProvider -> PostgreSQL users/user_identities
```

`SteamWebAPIProvider` validates configuration but deliberately returns
`ErrProviderUnavailable`; the Steam HTTP call and production wiring remain
future work. Never let a player select `player_id`, never send Steam Web API
keys to Unity, and never log auth tickets.

## Kubernetes scheduler and isolation

Each developer gets a stable hashed namespace. `EnsureTenant` creates:

- ResourceQuota: 4 CPU limits, 2 GiB memory limits, 20 pods;
- LimitRange defaults: 500m/256Mi/64Mi ephemeral limit and 100m/64Mi requests;
- default-deny ingress and egress;
- ingress to module port 50051 only from a namespace labeled
  `ruleshift.io/core=true`.

Each module version receives an immutable-image Deployment, ClusterIP Service,
and random RPC-token Secret. Pods run as UID 65532, non-root, read-only root
filesystem, RuntimeDefault seccomp, no privilege escalation, all capabilities
dropped, no service-account token, no host mounts, and no external egress.
Container limits are 500m CPU, 256Mi memory, and 64Mi ephemeral storage; requests
are 100m CPU and 64Mi memory.

Readiness uses Kubernetes gRPC health. Scheduler diagnostics bound messages to
1024 bytes and surface deployment/pod failures. Recoverable scheduling/image
failures and running-but-unready states have 30-second grace periods by default.
An inactive module workload may be deleted only when no room remains pinned to
it; active versions are preserved. Failed validation cleanup is currently the
only wired cleanup caller.

The gateway resolves a pinned workload's Service name and RPC token from
Kubernetes on demand and verifies the stored image digest. Do not treat a
database endpoint string as the source of truth.

## Observability and logging boundary

The public listener (default `:8080`) serves:

```text
GET /healthz
GET /readyz
/v2/developer/*
/v2/rooms*
/v2/ws
```

The private operations listener (default `127.0.0.1:9091`) serves:

```text
GET /healthz
GET /metrics                         # when metrics are enabled
GET /internal/v1/rooms               # only when public ref key is configured
GET /internal/v1/rooms/{public_ref}
```

Never expose the private listener publicly. `public_room_ref` is `rm_` plus
the lowercase unpadded Base32 encoding of the first 128 HMAC-SHA256 bits. The
key must contain at least 32 bytes and remain stable across restarts.

The public `observability-api` exposes only:

```text
GET /healthz
GET /v1/overview
GET /v1/rooms?cursor=&limit=&status=&module=&q=
GET /v1/rooms/{public_room_ref}
```

It runs fixed PromQL, never accepts arbitrary PromQL, caches overview results for
5 seconds, allows only the configured exact CORS origin, forwards only bounded
room filters, caps room pages at 100, and caps upstream bodies at 1 MiB.

Prometheus/Loki labels must remain bounded: component, operation, result, reason,
message type, direction, environment, or level are acceptable. Never label by
room, public ref, player, developer, module version, credential, or trace ID.

Logs are structured JSON. Never log authorization headers, API keys, registry
tokens, Steam tickets, player identities, protobuf payloads, authoritative
state, private projections, or database URLs. Internal room IDs must not appear
in public dashboards or public API responses.

Local observability Compose is under `deploy/observability` and assumes an
external `ruleshift-private` network. Production assets are under
`deploy/k3s/observability`; the example ingress is intentionally excluded from
the Kustomization until real DNS/TLS values are supplied.

## Standalone allocator and matchmaking libraries

These packages are real, tested code but are not part of the current gateway:

- `allocator.Registry` keeps in-memory server pools keyed by game/build,
  atomically reserves seats, is idempotent per match, releases or expires
  reservations, and removes unavailable servers from allocation;
- `matchmaking.Service` keeps in-memory tickets, matches, assignments, and
  events with the lifecycle
  `queued -> matched -> allocating -> assigned -> connecting -> in_game -> ended`
  plus failed/canceled/expired terminal paths;
- ticket creation is idempotent by player/pool/idempotency key and prevents a
  second active ticket for the same player/pool;
- `connecttoken.Manager` emits `rst1.<payload>.<HMAC-SHA256>` URL-safe tokens
  containing assignment, match, server, player, and expiry claims and requires a
  secret of at least 32 bytes.

Do not describe these as durable, distributed, or publicly available until they
gain persistence and composition/API wiring. Keep their capacity/state changes
under their mutexes and honor canceled contexts.

## Configuration

Gateway configuration is loaded in `internal/config`.

| Variable | Default | Notes |
| --- | --- | --- |
| `RULESHIFT_ADDR` | `:8080` | Public HTTP/WebSocket listener. |
| `RULESHIFT_OPERATIONS_ADDR` | `127.0.0.1:9091` | Private listener; never publish publicly. |
| `RULESHIFT_ENV` | `dev` | Structured-log environment. |
| `RULESHIFT_DATABASE_URL` | empty | Required by `cmd/gateway`; must select the control DB. |
| `RULESHIFT_DATABASE_ADMIN_URL` | derived | Optional admin connection used to create databases. |
| `RULESHIFT_MODULE_DATABASE_PREFIX` | `ruleshift_module_` | Must produce safe PostgreSQL identifiers. |
| `RULESHIFT_DEVELOPER_ID` | `default` | Bootstrapped tenant ID. |
| `RULESHIFT_DEVELOPER_NAME` | `Default developer` | Bootstrapped tenant display name. |
| `RULESHIFT_DEVELOPER_API_KEY` | empty | Optional bootstrap/update key; requires DB and at least 16 chars when used. |
| `RULESHIFT_KUBECONFIG` | empty | Empty means in-cluster config; set it outside Kubernetes. |
| `RULESHIFT_MAX_MESSAGE_BYTES` | 65536 | Full WebSocket envelope cap. |
| `RULESHIFT_ROOM_INPUT_QUEUE_SIZE` | 1024 | Per-room bounded queue. |
| `RULESHIFT_SESSION_SEND_QUEUE_SIZE` | 256 | Per-session bounded outbound queue. |
| `RULESHIFT_AUTH_TIMEOUT` | `5s` | Time allowed for first auth. |
| `RULESHIFT_READ_TIMEOUT` | `30s` | HTTP server timeout. |
| `RULESHIFT_WRITE_TIMEOUT` | `30s` | HTTP server timeout. |
| `RULESHIFT_SHUTDOWN_TIMEOUT` | `10s` | HTTP graceful shutdown. |
| `RULESHIFT_ENABLE_METRICS` | `true` | Controls private `/metrics`. |
| `RULESHIFT_ENABLE_PPROF` | `false` | Parsed only; pprof is not wired. |
| `RULESHIFT_PUBLIC_ROOM_REF_KEY` | empty | Empty disables private room diagnostics; configured value must be at least 32 bytes. |
| `RULESHIFT_QUEUE_DEGRADED_RATIO` | `0.8` | Must be in `(0,1]`. |

`cmd/observability-api` uses:

| Variable | Default |
| --- | --- |
| `OBS_ADDR` | `:8081` |
| `OBS_PROMETHEUS_URL` | `http://prometheus:9090` |
| `OBS_OPERATIONS_URL` | `http://ruleshift:9091` |
| `OBS_ALLOWED_ORIGIN` | `https://ruleshift.github.io` |
| `OBS_GRAFANA_SYSTEM_URL` | empty |
| `OBS_GRAFANA_RUNTIME_URL` | empty |
| `OBS_STALE_AFTER` | `30s` |
| `OBS_UNAVAILABLE_AFTER` | `60s` |
| `OBS_ERROR_RATIO_THRESHOLD` | `0.01` |
| `OBS_COMMAND_P95_THRESHOLD` | `250ms` |
| `OBS_QUEUE_SATURATION_THRESHOLD` | `0.8` |
| `OBS_SLOW_CONSUMER_RATIO_THRESHOLD` | `0.005` |
| `OBS_MIN_COMMANDS` | `100` |

`cmd/gamejam-promotions` uses:

| Variable | Default | Notes |
| --- | --- | --- |
| `GAMEJAM_PUBLIC_ADDR` | `:8082` | Public code verification listener. |
| `GAMEJAM_ADMIN_ADDR` | `:9092` | Basic-authenticated admin and metrics listener. |
| `GAMEJAM_DATABASE_URL` | empty | Required; selects the dedicated game-jam database. |
| `GAMEJAM_CODE_MASTER_KEY` | empty | Required base64 encoding of exactly 32 stable random bytes. |
| `GAMEJAM_ADMIN_USERNAME` | empty | Required Basic Auth username. |
| `GAMEJAM_ADMIN_PASSWORD_BCRYPT` | empty | Required bcrypt password hash. |
| `GAMEJAM_ALLOWED_ORIGIN` | `https://ruleshift.ru` | Exact custom-domain CORS origin for the GitHub Pages site. |
| `GAMEJAM_SYNC_INTERVAL` | `6h` | Discovery cadence; discovery also runs on startup. |
| `GAMEJAM_SOURCE_USER_AGENT` | Ruleshift bot identifier | Contact identity sent to source sites. |

Game-jam codes are shared and non-consuming, work only on inclusive Moscow
calendar dates for an approved event, and are never logged. The full code is
encrypted with AES-256-GCM and indexed by HMAC-SHA256 using keys derived from the
stable master key. Discovery classification never activates a promotion; admin
approval with an explicit eligibility reason is required.

## Local development

Required backend toolchain:

- Go 1.26;
- Docker and PostgreSQL 17 for the local database;
- access to a Kubernetes cluster plus kubeconfig when running outside a pod;
- `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` when changing schemas.

Start only PostgreSQL:

```powershell
docker compose up -d postgres
```

The root Compose file does not run the gateway or Kubernetes modules. A usable
local gateway needs at least:

```powershell
$env:RULESHIFT_DATABASE_URL = "postgres://ruleshift:ruleshift-dev@localhost:5432/ruleshift_control?sslmode=disable"
$env:RULESHIFT_KUBECONFIG = "<path-to-kubeconfig>"
$env:RULESHIFT_DEVELOPER_API_KEY = "<at-least-16-characters>"
go run ./cmd/gateway
```

The platform creates the selected control database through its admin connection.
Publishing or restoring module-backed rooms also requires the gateway process to
reach Kubernetes ClusterIP services; a kubeconfig alone does not guarantee that
network/DNS reachability.

Useful commands:

```powershell
go test ./...
go vet ./...
go test -race ./...
go test -bench ./...
go run ./cmd/observability-api
go run ./cmd/gamejam-promotions
dotnet pack sdk/dotnet/Ruleshift.Developer -c Release
```

Use `docker compose down -v` only when an intentional destructive reset of
local PostgreSQL data is wanted.

## Protobuf generation

Canonical schemas:

- `internal/protocol/proto/ruleshift.proto`;
- `internal/moduleruntime/proto/module_runtime.proto`.

On Windows:

```powershell
./scripts/proto.ps1
```

Generated Go packages are `ruleshiftv2` and `moduleruntimev2`, and generated
Go files are checked in. Commit schema and generated changes together. Do not
hand-edit generated files.

The Makefile's default `PROTOC` path is workstation-specific, and both the
Makefile and PowerShell script target
`sdk/unity/com.ruleshift.runtime/Runtime/Generated`, which is created on
demand and is not the checked-in Developer SDK directory. Verify the generated
diff instead of assuming the target is already present.

## Example module workflow

Each example has its own proto, manifest, Dockerfile, and conformance vectors.
Build from the repository root because the compact example host is shared:

```powershell
$env:RULESHIFT_DEVELOPER_API_KEY = "<developer-key>"
./scripts/publish-module.ps1 -ModuleId hiddennumber -DisplayName "Hidden Number" -RegistryRepository ghcr.io/ruleshift/hiddennumber -RegistryCredential main
```

The publication script:

- checks for required tools/files and matching manifest/Dockerfile versions;
- refuses to overwrite a non-active existing SemVer;
- optionally refreshes Docker and Ruleshift private-registry credentials;
- builds for the requested platform;
- extracts `/app/descriptor.pb` from the image;
- pushes the tag, resolves its digest, and publishes only the digest reference;
- requires both version and validation result to finish active.

Use `-PublicImage` only when the registry package is actually public. Example
module code may be compact or illustrative, but it must still satisfy ABI
determinism, authentication, health, type, payload, and conformance rules.

## Tests, benchmarks, and verification

After meaningful Go changes run:

```powershell
go test ./...
go vet ./...
```

For concurrency or hot-path changes also run:

```powershell
go test -race ./...
go test -bench ./...
```

Current tests cover protocol framing, room transitions/replay, module gRPC error
classification, conformance, control-plane validation/model rules, tenant
isolation, PostgreSQL migration metadata, Developer API rooms/handlers, metrics,
operations/public refs, observability API, SDK behavior, allocator,
matchmaking, connect tokens, and production import boundaries. Kubernetes tests
use the fake client. PostgreSQL migration tests inspect embedded schemas and do
not require a live database.

Current explicit benchmarks cover protobuf codec, allocator reservation, and
matchmaking. Add focused allocation/latency benchmarks before making hot-path
performance claims. Do not present `docs/performance-report.md` planned numbers
as measured results.

For `site/`, follow `site/AGENTS.md`, run `npm ci` when dependencies are
needed, then `npm run build`, and visually verify substantial UI changes at the
documented desktop/mobile sizes.

## CI/CD and deployment

`.github/workflows/deploy.yml` runs on `main` and manual dispatch:

1. `go test ./...`;
2. run the game-jam PostgreSQL integration test;
3. build gateway, observability, and game-jam promotion images through the
   common Dockerfile using the corresponding `BINARY=./cmd/...` value;
4. push `latest` and immutable commit-SHA tags to GHCR;
5. when `K3S_AUTO_DEPLOY=true`, SSH to the VPS and invoke
   `ruleshift-update-gateway ghcr.io/ruleshift/server:<40-char-sha>`.

`scripts/update-gateway.sh` validates the immutable SHA/digest reference,
performs rollout health checks, and rolls back on failure. The VPS does not need
a Git checkout.

`.github/workflows/pages.yml` builds `site/` with Node 22 and deploys
`site/dist` to GitHub Pages when site files change.

Do not put production secrets, kubeconfigs, registry credentials, Grafana
passwords, public-room HMAC keys, SSH passwords, or generated secret manifests
in Git.

## Documentation expectations

README is interviewer-facing and must continue to explain:

- what Ruleshift is and why the server is authoritative;
- what is actually implemented;
- how the protobuf command/state flow works;
- how to run and test it;
- why bounded sequential runtimes and opaque modules matter for performance and
  extensibility;
- what remains incomplete.

Keep these contracts synchronized with code:

- player protocol: `docs/protocol.md`, `docs/ru/protocol.md`, player proto,
  Unity player guide;
- module ABI/lifecycle: architecture and module-development docs, ABI proto,
  manifests, examples;
- Developer API: both language docs, OpenAPI, Go/Unity/.NET SDKs;
- storage: database docs and embedded migrations;
- observability: observability docs, OpenAPI, Prometheus rules, Grafana
  dashboards, Compose/k3s manifests;
- config/deployment: README, Dockerfile, scripts, and workflow files.

Do not claim planned features are production-ready. Prefer an explicit
“implemented / library-only / future” distinction.

## Change checklists

For room/runtime changes:

- preserve single-owner sequential state mutation;
- test changed=false, rejection, timeout, persistence failure, projection
  failure, revision mismatch, replay digest mismatch, shutdown, and queue
  pressure where relevant;
- confirm no player I/O enters the room loop;
- update metrics without high-cardinality labels.

For protocol changes:

- edit canonical proto, regenerate checked-in bindings, add codec/gateway tests,
  update docs and player examples;
- keep binary one-envelope-per-frame behavior and explicit versioning;
- consider both the module payload limit and full WebSocket envelope limit.

For module/control-plane changes:

- validate manifests/descriptors before scheduling;
- preserve immutable digest and room pinning semantics;
- run conformance twice;
- keep registry and RPC secrets out of PostgreSQL/logs/responses;
- keep failed-validation cleanup bounded.

For storage changes:

- add migrations; never rewrite applied files;
- keep control/module DB responsibilities separate;
- preserve atomic state/event/snapshot commit and digest validation;
- use safe identifiers and parameterized values; never accept raw SQL from a
  developer.

For deployment/security changes:

- preserve non-root/read-only/seccomp/no-capabilities/no-token defaults;
- preserve default-deny networking and private operations surfaces;
- use immutable image references and verify rollback behavior.

For public API/SDK changes:

- keep tenant scoping and bounded bodies/responses;
- update OpenAPI and all applicable SDKs together;
- never make a Developer API key necessary in a player build.

## Known traps for future agents

- Package existence does not mean production wiring; verify `cmd/gateway`.
- The gateway cannot start with only root `docker compose up`: it requires a
  control DB URL and usable Kubernetes config.
- Empty `RULESHIFT_KUBECONFIG` means in-cluster config, not a local default.
- Default WebSocket frame size (64 KiB) is lower than ABI message/view limits.
- `last_seen_revision` is currently ignored.
- Invite codes currently have no resolution/join API.
- `ErrQueueFull` is declared but queue submission currently waits.
- Projection failure can happen after a transition has committed.
- Health/readiness are not dependency-aware.
- pprof config exists without handlers.
- Steam provider and botload are stubs.
- Public room diagnostics disappear when the HMAC key is unset.
- The observability site and observability API are separate; the current static
  site does not consume the API.
- No explicit Kubernetes replica count is intentional MVP behavior; do not add
  an HA claim without implementing and testing a policy.
- Generated files, Russian/English docs, SDKs, OpenAPI, deployments, and example
  manifests can drift unless changed together.
