# Ruleshift protocols

Ruleshift has two independent protobuf contracts.

## Player WebSocket v2

Schema: `internal/protocol/proto/ruleshift.proto`. Endpoint: `/v2/ws`. Each
binary WebSocket frame is exactly one serialized envelope; there is no extra
length prefix. `protocol_version` must equal `2`; v1 is rejected.

The sequence is:

1. `AuthRequest` must be the first envelope.
2. `JoinRoomRequest.invite_code` contains the six-character code returned by
   `POST /v2/rooms`; the server resolves a non-expired code to its internal room ID.
3. `JoinRoomOk.room_id` returns that internal ID and the server sends a
   recipient-specific `StateSnapshot`.
4. The player sends `GameCommand` with authenticated intent in `Any`.
5. The server serializes the command through the room queue and broadcasts
   recipient-specific `StateDelta` messages in revision order.

Core messages contain no game enums or game oneofs. `ModuleRef` identifies the
pinned module/version, while state, command and delta use `google.protobuf.Any`.
`view_digest` is SHA-256 of the exact projected protobuf payload.

An error or timeout from a module produces `module_unavailable` or
`command_rejected` and never changes room revision.

## Module Runtime ABI v1

Schema: `internal/moduleruntime/proto/module_runtime.proto`. It is ordinary gRPC
on port 50051 inside the Kubernetes cluster. The service implements `Describe`,
`NewState`, player lifecycle, `Apply`, and projection RPCs.

Every request contains an `operation_id`; every transition is stateless and
side-effect free. Ruleshift supplies authenticated `player_id`, server time,
room revision and deterministic room seed. The module returns opaque protobuf
bytes; Ruleshift owns persistence and revision ordering.

Limits:

- state: 1 MiB;
- command, delta, view: 256 KiB;
- transition deadline: 50 ms by default, never above 250 ms;
- `NewState`: at most 250 ms;
- one retry only for `Unavailable` within the original deadline.

Module traffic uses a random per-deployment bearer token stored in Kubernetes
Secret. Player and developer credentials are never forwarded to module pods.

## Generation

```powershell
./scripts/proto.ps1
```

Generated Go packages are `ruleshiftv2` and `moduleruntimev1`. Module authors
generate ABI bindings and their own game proto bindings in their own repository.
