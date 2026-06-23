# Protocol

The MVP protocol uses WebSocket as transport and binary protobuf as payload.

## Wire Format

Each WebSocket binary message carries one serialized protobuf envelope. WebSocket framing provides message boundaries, so there is no extra length prefix inside the payload.

## Envelopes

All client messages use `ClientEnvelope`.

All server messages use `ServerEnvelope`.

The schema is in `internal/protocol/proto/ruleshift.proto`.

Current protobuf package:

```proto
package ruleshift.v1;
option go_package = "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1;ruleshiftv1";
option csharp_namespace = "Ruleshift.Protocol.V1";
```

## Versioning

`protocol_version` starts at `1`. The gateway rejects unsupported protocol versions before dispatching client payloads.

## Revision Model

Room state is:

- `room_id`
- `revision uint64`
- module-owned game state. The current `GAME_TYPE_XIANGQI` snapshot includes FEN, packed board pieces, side to move, seated player ids, game status, and state hash.
- a recipient-specific `view_hash`, calculated only from fields visible to that recipient.

Every accepted `GameCommand` increments revision by exactly one. Seating a new player also advances revision because it changes authoritative game state. All clients observe the same ordered revisions, but their projected payloads and `view_hash` values can differ. `no_visible_change` advances a recipient through a revision that changed no visible fields.

## Game Commands

Clients send `GameCommand` after auth and room join:

- `DoMove` carries either compact square indexes (`from_square`, `to_square`, 0..89) or a UCI-style move string such as `h2e2`.
- `Resign` marks the game resigned and records the winner when the player is seated.
- `OfferDraw` records a draw offer; a later opponent offer accepts the draw.
- `SetSecret` is used by the Hidden Number demo and is never echoed to unauthorized views.

The room runtime validates `room_id` and `expected_revision`. The Xiangqi module validates seating, side to move, and legal moves through the engine before mutating state.

## Join And Resume

`JoinRoomRequest.join_mode` selects a player or spectator connection. The gateway, not the client, derives the effective `ViewScope`:

- `PLAYER` sees public state plus that player's private fields;
- `PUBLIC` is the default spectator view and contains no private fields;
- `FULL` is available only to a spectator with a server-authenticated full-view permission.

Every join and reconnect returns `JoinRoomOk` followed by a projected `StateSnapshot`, even when `last_seen_revision` matches. The field remains reserved for a future delta-history resume path. Reconnecting with the same authenticated `player_id` replaces the previous session and prevents the old session from mutating state.

Canonical snapshots, deltas, and state hashes are server-only data used for durable events and replay. They must never be serialized as a fallback when projection fails.

## Generation Plan

Install:

- `protoc`
- `protoc-gen-go`

Then run:

```powershell
.\scripts\proto.ps1
```

Equivalent explicit commands:

```powershell
protoc -I . --go_out=. --go_opt=module=github.com/Ruleshift/server internal/protocol/proto/ruleshift.proto
protoc -I . --csharp_out=unity-client/Assets/Scripts/Network/Generated internal/protocol/proto/ruleshift.proto
```

`protoc` is installed through WinGet. The repository includes `scripts/proto.ps1`, which prepends the WinGet install path before running generation, so codegen works even if the already-running shell has not refreshed PATH.

## Generated Bindings

The generated Go package is:

```go
github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1
```

Gateway code uses `internal/protocol` encode/decode helpers, which delegate directly to the generated protobuf runtime.

## Error Handling

Oversized WebSocket frames are rejected by the transport. Malformed protobuf payloads are rejected while decoding `ClientEnvelope`. The gateway and room runtime reject invalid application requests:

- unsupported protocol version;
- missing payload;
- unexpected payload for the connection state;
- empty room IDs or auth tickets;
- invalid or illegal game command;
- stale replaced sessions;
- stale or mismatched room revisions.


