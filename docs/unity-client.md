# Unity Client Integration

The Unity client skeleton lives under `unity-client/Assets/Scripts/Network`.

## Dependencies

Planned dependencies:

- Google.Protobuf for generated C# messages.
- NativeWebSocket installed through Unity Package Manager from `https://github.com/endel/NativeWebSocket.git#upm`.
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

`MatchClient` uses NativeWebSocket for transport. Call `DispatchMessageQueue()` from a Unity `Update()` loop so NativeWebSocket can deliver queued messages on non-WebGL builds.

Generated protobuf files will be placed in `unity-client/Assets/Scripts/Network/Generated`.

## Steam Auth

For production Steam auth, Unity should obtain a Steam auth session ticket from Steamworks.NET or another Steamworks bridge and send only that ticket in `AuthRequest.ticket`. The Steam Web API key stays on the Go gateway in `RULESHIFT_STEAM_WEB_API_KEY`; it must never be included in Unity code or client configuration.


