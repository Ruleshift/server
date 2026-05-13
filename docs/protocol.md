# Protocol

The MVP protocol uses WebSocket as transport and binary protobuf as payload.

## Frame Format

Each WebSocket binary payload carries:

```text
uint32 length prefix, big-endian
protobuf payload bytes
```

The current skeleton implements length-prefix validation in `internal/protocol.FrameCodec`.

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

`protocol_version` starts at `1`. The gateway will reject unsupported protocol versions in phase 5.

## Revision Model

Room state is:

- `room_id`
- `value int64`
- `revision uint64`

Every accepted command increments revision by exactly one. Clients observe ordered deltas or a recovery snapshot.

## Generation Plan

Install:

- `protoc`
- `protoc-gen-go`

Then run:

```powershell
make proto
```

Equivalent explicit commands:

```powershell
protoc -I . --go_out=. --go_opt=module=github.com/Ruleshift/server internal/protocol/proto/ruleshift.proto
protoc -I . --csharp_out=unity-client/Assets/Scripts/Network/Generated internal/protocol/proto/ruleshift.proto
```

In the current local environment `protoc-gen-go` is available, but `protoc` is not visible in PATH. Phase 2 includes the schema, generation targets, and Go protocol wrapper validation while leaving generated files out of the repository.

## Go Wrapper Layer

Until generated code is available, `internal/protocol` exposes small wrapper structs matching the schema shape:

- `ClientEnvelope`
- `ServerEnvelope`
- `AuthRequest`
- `JoinRoomRequest`
- `IntCommand`
- `SnapshotRequest`
- `StateSnapshot`
- `StateDelta`

The wrapper layer validates protocol version, required payloads, known operations, and unknown payloads. It is intentionally narrow and will be replaced at gateway boundaries by generated protobuf messages in the next implementation step.

## Error Handling

Malformed frames are rejected before protobuf decode. Invalid envelopes are rejected after decode:

- unsupported protocol version;
- missing payload;
- unknown payload;
- empty required fields;
- invalid integer operation;
- non-increasing delta revision.


