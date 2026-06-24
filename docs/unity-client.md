# Unity player client

The player transport uses binary protobuf WebSocket protocol v2 at `/v2/ws`.
Generate C# bindings from `internal/protocol/proto/ruleshift.proto` and generate
the selected module's own protobuf bindings in the game project.

## Flow

1. Connect to `ws://host:port/v2/ws`.
2. Send `ClientEnvelope` with `protocol_version = 2` and `AuthRequest`.
3. Join a room previously created by a trusted backend through `POST /v2/rooms`.
4. Read `ModuleRef` and unpack snapshot/delta `Any` values with module bindings.
5. Pack module commands into `Any` and send `GameCommand` with the latest
   observed revision.
6. Apply snapshots and deltas only in server revision order.

The developer API key must never ship in Unity player builds. It belongs only
in Editor tooling, CI, or a trusted backend.

```csharp
var envelope = new ClientEnvelope {
    ProtocolVersion = 2,
    ClientSequence = ++sequence,
    GameCommand = new GameCommand {
        RoomId = roomId,
        ExpectedRevision = revision,
        Command = Any.Pack(moduleCommand)
    }
};
```

`view_digest` is SHA-256 of the exact projected protobuf payload. It can be used
to detect client desynchronization without exposing canonical private state.
