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
- invalid integer operation;
- stale or mismatched room revisions.


