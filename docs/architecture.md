# Ruleshift Architecture

Ruleshift is an authoritative multiplayer state server. The future domain is a Steam-compatible Unity C# card game, but the MVP state is intentionally one shared `int64` per room.

## Phase Plan

- Phase 0: repository discovery and project plan.
- Phase 1: project skeleton with Go module, entrypoints, config, logging, and package boundaries.
- Phase 2: protobuf schema, generation targets, framing, and protocol validation fallback.
- Phase 3: authoritative integer room model with sequential runtime tests.
- Phase 4: actor-like runtime hardening with full bounded queues and slow-consumer handling.
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

`cmd/gateway` owns HTTP server setup and transport wiring. It should stay thin: decode envelopes, validate session state, call auth, submit room commands, and write server envelopes.

### Auth

`internal/auth` exposes a small `Provider` interface. Room logic receives server-side player identity only after successful authentication. Steam integration remains replaceable and is not coupled to room state.

### Protocol

`internal/protocol` owns binary framing and protobuf schema. Every client message flows through `ClientEnvelope`; every server message flows through `ServerEnvelope`.

### RoomRegistry

`internal/room.Registry` maps room IDs to `RoomRuntime` instances. It creates rooms lazily for MVP join behavior.

### RoomRuntime

One `RoomRuntime` owns one `RoomState`. Commands enter through a bounded input queue and are applied sequentially. External packages request snapshots or submit commands through the runtime, rather than mutating room state directly.

Phase 3 behavior:

- `Add` increases the shared integer value.
- `Set` replaces the shared integer value.
- each accepted command increments revision exactly once;
- invalid operations are rejected without changing state;
- `expected_revision == 0` means blind update;
- non-zero `expected_revision` must match current room revision.

### State Replication

Successful commands produce a monotonically increasing revision and a `StateDelta`. Joined clients are represented by a small room-local `DeltaSink` interface. The runtime sends the same delta object to every joined sink, keeping network implementation out of room logic.

Joining clients receive a `StateSnapshot`. Reconnect-specific resume behavior is planned for phase 7.

### Backpressure

Network sessions will own bounded send queues. Room runtime must never block forever on network writes; slow consumers are handled by queue policy and eventual disconnect. Phase 3 uses `TrySendDelta` on `DeltaSink`; Phase 4 will define the concrete slow-consumer policy.

### Reconnect

Clients keep `last_seen_revision`. On join/resume the server compares it with current room revision and sends a snapshot if needed. Session replacement is planned for phase 7.

### Event Log And Replay

Replay is planned to demonstrate event-sourcing-style recovery. The current skeleton includes delta replay helpers; phase 8 will add append-only room events and an in-memory event store.

## Non-Goals For MVP

- Cards, decks, hands, abilities, combat rules, or game balance.
- Trusting client state as authoritative.
- FlatBuffers.
- Secret storage in repo.


