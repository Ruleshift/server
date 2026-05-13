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
.\scripts\proto.ps1
```

Equivalent explicit commands:

```powershell
protoc -I . --go_out=. --go_opt=module=github.com/Ruleshift/server internal/protocol/proto/ruleshift.proto
protoc -I . --csharp_out=unity-client/Assets/Scripts/Network/Generated internal/protocol/proto/ruleshift.proto
```

`protoc` is installed through WinGet. The repository includes `scripts/proto.ps1`, which prepends the WinGet install path before running generation, so codegen works even if the already-running shell has not refreshed PATH.

## Go Wrapper Layer

The generated Go package is:

```go
github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1
```

`internal/protocol` keeps only the length-prefix frame codec and validation helpers around generated messages.

## Error Handling

Malformed frames are rejected before protobuf decode. Invalid envelopes are rejected after decode:

- unsupported protocol version;
- missing payload;
- unknown payload;
- empty required fields;
- invalid integer operation;
- non-increasing delta revision.


