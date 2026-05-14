# Unity Client Integration

The Unity client skeleton lives under `unity-client/Assets/Scripts/Network`.

## Dependencies

- Google.Protobuf for generated C# messages.
- NativeWebSocket installed through Unity Package Manager from `https://github.com/endel/NativeWebSocket.git#upm`.
- Steamworks.NET or another Steamworks bridge for production Steam tickets.

## Flow

1. Connect to `ws://host:port/ws`.
2. Send `AuthRequest` with `mock:player-1` locally.
3. Receive `AuthOk`.
4. Send `JoinRoomRequest` with the client's stored `last_seen_revision`.
5. Receive `JoinRoomOk` and, when that stored revision differs from the server revision, `StateSnapshot`.
6. Send `IntCommand` add/set operations.
7. Apply `StateDelta` in revision order.
8. Store `lastSeenRevision` for reconnect.

## Skeleton Files

- `MatchClient.cs` owns connection state, sends `AuthRequest`, `JoinRoomRequest`, Add/Set/Get commands, applies snapshots/deltas, and keeps `LastSeenRevision` for `ReconnectAsync`.
- `ProtocolCodec.cs` serializes `ClientEnvelope` and deserializes `ServerEnvelope` with Google.Protobuf. WebSocket sends raw protobuf bytes; the codec also includes length-prefix helpers for future transports that do not preserve message boundaries.
- `MockAuthProvider.cs` returns `mock:player-1` style tickets for the Go mock provider. Replace it with Steamworks.NET or another Steamworks bridge in production.

## Minimal Usage

```csharp
var client = new MatchClient();
var auth = new MockAuthProvider("player-1");

await client.ConnectAsync(new Uri("ws://localhost:8080/ws"), cancellationToken);
await client.SendAuthRequestAsync(auth.GetTicket(), cancellationToken);
await client.JoinRoomAsync("room-1", cancellationToken);
await client.SendAddAsync(5, cancellationToken);
await client.SendSetAsync(42, cancellationToken);
var currentValue = await client.GetAsync(cancellationToken);

// Later, after a transport drop, this rejoins with the stored LastSeenRevision.
await client.ReconnectAsync(new Uri("ws://localhost:8080/ws"), auth.GetTicket(), cancellationToken);
```

`GetAsync` sends a `SnapshotRequest`, waits for the server's `StateSnapshot`, stores the returned revision, logs the current value with `Debug.Log`, and returns the `long` value. The `Get()` convenience method does the same work with `CancellationToken.None` for simple Unity scripts or button handlers.

`MatchClient` uses NativeWebSocket for transport. Call `DispatchMessageQueue()` from a Unity `Update()` loop so NativeWebSocket can deliver queued messages on non-WebGL builds.

Generated protobuf files will be placed in `unity-client/Assets/Scripts/Network/Generated`.


