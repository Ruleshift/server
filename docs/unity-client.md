# Unity Client Integration

The Unity client skeleton lives under `unity-client/Assets/Scripts/Network`.

## Dependencies

Planned dependencies:

- Google.Protobuf for generated C# messages.
- ClientWebSocket or a Unity-compatible WebSocket package.
- Steamworks.NET or another Steamworks bridge for production Steam tickets.

## Flow

1. Connect to `ws://host:port/ws`.
2. Send `AuthRequest` with `mock:player-1` locally.
3. Receive `AuthOk`.
4. Send `JoinRoomRequest`.
5. Receive `JoinRoomOk` and, if needed, `StateSnapshot`.
6. Send `IntCommand` add/set operations.
7. Apply `StateDelta` in revision order.
8. Store `lastSeenRevision` for reconnect.

Generated protobuf files will be placed in `unity-client/Assets/Scripts/Network/Generated`.


