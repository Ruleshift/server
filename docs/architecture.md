# Ruleshift Architecture

Ruleshift is an authoritative multiplayer state server. The future domain is a Steam-compatible Unity C# card game, but the current MVP room state is a pluggable game module. The first module is Xiangqi.

## Phase Plan

- Phase 0: repository discovery and project plan.
- Phase 1: project skeleton with Go module, entrypoints, config, logging, and package boundaries.
- Phase 2: protobuf schema, generation targets, and direct protobuf WebSocket payloads.
- Phase 3: authoritative game room model with sequential runtime tests.
- Phase 4: actor-like runtime hardening with bounded session queues and slow-consumer handling.
- Phase 5: WebSocket gateway.
- Phase 6: Steam-compatible authentication.
- Phase 7: reconnect and snapshot resume.
- Phase 8: event log and replay.
- Phase 9: Unity C# client skeleton.
- Phase 10: observability and performance measurement.
- Phase 11: bot load test.
- Phase 12: testing and benchmark strategy.
- Phase 13: interview polish.

## Components

### Gateway

`cmd/gateway` owns HTTP server setup. `internal/gateway` owns the Gorilla WebSocket state machine: read binary protobuf envelopes, validate session state, call auth, submit room commands, and write server envelopes. Gateway websocket sessions own bounded outbound queues and implement `room.PlayerSink`.

Phase 5 gateway behavior:

- `/healthz` returns a simple HTTP health response;
- `/readyz` returns a simple readiness response;
- optional `/metrics` returns a minimal text-format process and room-count signal;
- `/ws` upgrades to WebSocket;
- WebSocket messages are binary;
- binary payloads are protobuf envelopes serialized directly with the generated protobuf runtime;
- `AuthRequest` must be the first client envelope;
- `JoinRoomRequest` creates or joins a room and returns `JoinRoomOk`, followed by `StateSnapshot` only when the client's `last_seen_revision` differs from the current room revision;
- `GameCommand` is submitted to the authoritative room runtime;
- successful commands are broadcast as `StateDelta` to all joined sessions;
- app-level `Ping` returns `Pong`;
- basic `client_sequence` monotonic checks reject repeated or out-of-order client messages.

### Auth

`internal/auth` exposes a small `Provider` interface. Room logic receives server-side player identity only after successful authentication. Steam integration remains replaceable and is not coupled to room state.

### Matchmaking

`internal/matchmaking` owns the new ticket-to-assignment orchestration model. It keeps in-memory indexes for queued tickets by game/build pool, active tickets by player/pool, idempotency keys, matches, assignments, and lifecycle audit events. The state machine follows `queued -> matched -> allocating -> assigned -> connecting -> in_game -> ended`, with terminal `failed`, `canceled`, and `expired` states.

Current behavior:

- ticket creation is idempotent by explicit key and by active player/game/build ticket;
- queued tickets can be canceled by the owning player;
- match formation consumes only queued tickets from the same game/build pool;
- assignment TTL expiration fails the match and releases server reservations;
- server failure marks affected assigned matches as failed;
- lifecycle events include ticket, match, server, and player identifiers, but never full connect tokens.

This layer is not wired to public API handlers yet; it is ready to sit behind HTTP or protobuf endpoints in a later iteration.

### Allocator

`internal/allocator` owns in-memory server registration and atomic seat reservation. It indexes servers by game/build pool and performs reserve/release under one mutex, so a server seat cannot be assigned to two matches. Reservation retry is idempotent by `match_id`, and reservation TTL cleanup releases seats.

### Connect Tokens

`internal/connecttoken` creates and validates HMAC-signed connect tokens. A token contains assignment, match, server, player, and expiry claims. Validation rejects expired, malformed, or tampered tokens before the game server accepts a connection.

### Protocol

`internal/protocol` owns protobuf schema, generated bindings, protocol versioning, and protobuf encode/decode helpers. Every client message flows through `ClientEnvelope`; every server message flows through `ServerEnvelope`.

### RoomRegistry

`internal/room.Registry` maps room IDs to `RoomRuntime` instances. It creates rooms lazily for MVP join behavior.

### RoomRuntime

One `RoomRuntime` owns one `RoomState`. Commands enter through a bounded input queue and are applied sequentially. External packages request snapshots or submit commands through the runtime, rather than mutating room state directly.

Current behavior:

- `internal/room` owns queueing, revisions, snapshots, subscribers, and replay.
- `internal/game` defines the game module contract and abstract command/snapshot/delta envelopes.
- Concrete modules own their payload structs; `internal/game/xiangqi` embeds the abstract envelopes for Xiangqi moves, snapshots, deltas, sides, and board updates.
- `internal/game/xiangqi` adapts `github.com/laines-it/xiangqi-go/engine` via its declared module path and applies legal `DoMove` commands through `GenerateLEGAL` and `DoMove`.
- first and second joined players are seated as red and black; further joined clients can observe.
- accepted `DoMove`, `Resign`, and `OfferDraw` commands increment revision exactly once;
- invalid game commands are rejected without changing state;
- `expected_revision == 0` means blind update;
- non-zero `expected_revision` must match current room revision.

### State Replication

Successful commands produce a monotonically increasing revision and a `StateDelta`. Joined clients are represented inside room logic by the small `PlayerSink` interface. The runtime sends the same protobuf envelope to every joined sink, keeping WebSocket implementation details out of room logic.

Joining clients receive `JoinRoomOk`. They receive a `StateSnapshot` only when their `last_seen_revision` does not match the room's current revision. The MVP uses snapshots for recovery rather than replaying a per-room delta history.

### Backpressure

Gateway websocket sessions own bounded outbound queues. Runtime broadcast uses only non-blocking sends through `PlayerSink`:

- first delta overflow increments a slow-consumer strike and compacts queued deltas into a fresh `StateSnapshot`;
- repeated overflow closes the session with `slow_consumer`;
- closed sessions are removed from the room subscriber set;
- runtime shutdown closes joined sessions with `shutdown`.

The WebSocket writer loop drains bounded session queues outside the room runtime, so room logic avoids blocking on network writes.

### Reconnect

Clients keep `last_seen_revision`. On join/resume the server compares it with current room revision and sends a snapshot if needed. Reconnecting with the same authenticated `player_id` replaces the previous session, closes it with `replaced`, and room command submission checks the current session id before mutating state. The player id comes from the auth provider, not from client command payloads.

### Event Log And Replay

Room runtimes can write append-only `RoomEvent` records to an `EventStore`. The MVP includes `InMemoryEventStore`, which assigns a monotonic sequence number at append time and returns room-filtered copies for replay.

Event types are:

- `RoomCreated`
- `PlayerJoined`
- `GameMoveApplied`
- `PlayerResigned`
- `DrawOffered`
- `SnapshotSent`
- `PlayerDisconnected`

Replay starts from `RoomCreated`, applies `PlayerJoined` to restore module seating, then reapplies mutating game events through the same module contract. Snapshot and disconnect events remain audit trail entries. This restores the authoritative `RoomState` and revision without trusting client state. The current implementation is intentionally in-memory for interview clarity; a file-backed or durable store can be added behind the same `EventStore` interface later.

## Non-Goals For MVP

- Cards, decks, hands, abilities, combat rules, or game balance.
- Trusting client state as authoritative.
- FlatBuffers.
- Secret storage in repo.


