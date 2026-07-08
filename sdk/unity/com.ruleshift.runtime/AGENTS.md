# AGENTS.md - Unity Player Transport

## Scope

These instructions apply to `sdk/unity/com.ruleshift.runtime`.

Build only the runtime portion of the Unity client that connects to Ruleshift,
sends player requests, and receives enough server envelopes to maintain
connection state and the authoritative room revision. Keep game presentation,
game reducers, matchmaking UI, Steam ticket acquisition, and Developer API
operations outside this package.

## Authoritative contracts

- Player protocol: `internal/protocol/proto/ruleshift.proto`.
- Endpoint: `/v2/ws`; production uses `wss://`.
- Generated C# protobuf files belong under `Runtime/Generated` and are produced
  by `scripts/proto.ps1`.
- Never edit generated protobuf files manually.
- Each WebSocket binary message contains exactly one serialized
  `ruleshift.v2.ClientEnvelope` or `ServerEnvelope`. Do not add a length prefix,
  JSON wrapper, Base64 wrapper, or custom framing.
- Every outgoing envelope has `protocol_version = 2`.

If implementation assumptions disagree with the protobuf schema, the schema
wins and the surrounding documentation must be corrected.

## Required client flow

Implement and enforce this state machine:

1. Open the WebSocket.
2. Send `AuthRequest` as the first envelope.
3. Wait for `AuthOk`; stop on `AuthFailed`.
4. Send `JoinRoomRequest` only after authentication succeeds.
5. Wait for `JoinRoomOk` before allowing commands or snapshot requests.
6. Track the latest authoritative revision from `JoinRoomOk`,
   `StateSnapshot`, and `StateDelta`.
7. Pack a developer-owned module command in `google.protobuf.Any` and send it
   as `GameCommand` with the latest observed `expected_revision`.
8. Send `SnapshotRequest` when recovery is required.

Use explicit states such as `Disconnected`, `Connecting`, `Authenticating`,
`Authenticated`, `Joining`, `Joined`, `Reconnecting`, `Faulted`, and
`Disposed`. Reject invalid operations locally instead of sending them in the
wrong state.

## Sequencing and revisions

- `client_sequence` is strictly increasing and non-zero for one WebSocket
  connection. Assign it in the single writer loop so concurrent callers cannot
  reorder it.
- Reset `client_sequence` only after opening a new WebSocket connection.
- Maintain exactly one ordered reader loop and one serialized writer loop.
- A `StateDelta` is applicable only when `previous_revision` equals the current
  revision. On a gap, pause gameplay sends and request a snapshot.
- Never trust or accept a revision supplied by game UI code.
- Never let the local game state advance merely because a command was sent.
  Only server snapshots and deltas advance authoritative revision.
- Do not automatically retry `GameCommand` after an uncertain disconnect. The
  current player protocol has no command idempotency key, so replay could apply
  an action twice.

## Reconnect behavior

- Reconnect with a new socket, reset the per-connection sequence, authenticate
  again, and join using the latest safely applied revision as
  `last_seen_revision`.
- Be prepared to receive a fresh snapshot even when a last-seen revision was
  supplied.
- Keep outbound gameplay disabled until authentication, join, and revision
  recovery complete.
- Use caller-provided `CancellationToken` values for connection lifetime and
  operations. Do not hide unbounded retry loops inside the package.
- Backoff policy must be bounded and configurable. It must stop on cancellation
  and on permanent authentication or protocol failures.

## Transport design

- Define a small WebSocket abstraction so protocol logic is testable without a
  network and so WebGL can receive a separate adapter later.
- A native/desktop adapter may use `System.Net.WebSockets.ClientWebSocket`.
- Do not claim WebGL support unless a browser-compatible adapter and tests are
  actually present.
- Do not call Unity APIs from network threads. Marshal callbacks through a
  caller-provided dispatcher or synchronization context.
- All queues are bounded and expose an explicit capacity in options.
- Queue saturation must fail predictably with a typed error; never create an
  unbounded queue and never block the Unity main thread waiting for capacity.
- Permit at most one active send and one active receive on the underlying
  WebSocket.
- Accept binary frames only. Treat text frames, malformed protobuf, unsupported
  protocol versions, and decreasing server sequence values as protocol errors.
- Set bounded frame sizes compatible with the gateway configuration. Reject an
  oversized payload before enqueueing it.

## Suggested public API

Keep the public surface small and async:

```csharp
Task ConnectAndAuthenticateAsync(Uri endpoint, string playerTicket,
    CancellationToken cancellationToken);
Task JoinRoomAsync(string roomId, JoinMode mode,
    CancellationToken cancellationToken);
ValueTask SendCommandAsync(Google.Protobuf.WellKnownTypes.Any command,
    CancellationToken cancellationToken);
ValueTask RequestSnapshotAsync(CancellationToken cancellationToken);
Task DisconnectAsync(CancellationToken cancellationToken);
```

Also expose read-only connection state, authenticated player identity, joined
room, pinned `ModuleRef`, view scope, and latest authoritative revision.
Events or callbacks may expose snapshots, deltas, errors, and state changes,
but callbacks must not execute while internal locks are held.

The runtime package remains game-agnostic. It accepts `Any` or an `IMessage`
that it packs into `Any`; it must not reference Hidden Number, Xiangqi, Card
Game, or any other module schema.

## Security boundary

- Player builds contain only player authentication tickets.
- Never add `RULESHIFT_DEVELOPER_API_KEY`, registry credentials, Kubernetes
  credentials, or Developer API calls to this package.
- Do not log player tickets, authorization headers, complete command payloads,
  private snapshots, or private deltas.
- Require `wss://` outside an explicit local-development mode limited to
  loopback addresses.
- Use platform certificate validation. Do not add a certificate-validation
  bypass or a "trust all certificates" option.
- Treat `room_id`, `player_id`, module type URLs, and all received payloads as
  untrusted input until protobuf parsing and local bounds checks succeed.

## Errors

Distinguish at least:

- local invalid-state or invalid-argument errors;
- queue saturation;
- transport disconnect/cancellation;
- authentication failure;
- server `ErrorMessage` responses;
- protobuf/protocol violations;
- revision gaps requiring snapshot recovery.

Surface server error code and message without converting server failure into a
local state mutation. `module_unavailable` and `command_rejected` never advance
the revision.

## File boundaries

Prefer this layout:

```text
Runtime/Generated/                 generated protobuf only
Runtime/Transport/                 WebSocket interface and platform adapters
Runtime/Internal/                  bounded queues, reader/writer loops
Runtime/RuleshiftClient.cs         public orchestration API
Runtime/RuleshiftClientOptions.cs  limits and reconnect configuration
Runtime/RuleshiftConnectionState.cs
Tests/Runtime/                     edit-mode unit tests with a fake transport
```

Keep transport/session code independent of MonoBehaviour. Optional Unity
components may wrap the plain C# client in separate files.

## Mandatory tests

Use a deterministic fake WebSocket transport and verify:

- auth is always the first envelope;
- every frame is binary protobuf without extra framing;
- protocol version is always 2;
- client sequences remain strictly increasing under concurrent sends;
- join cannot precede `AuthOk`;
- commands cannot precede `JoinRoomOk`;
- commands carry the current server revision and caller-supplied `Any`;
- sending a command does not advance local revision;
- snapshots and ordered deltas advance revision correctly;
- a delta gap pauses commands and requests recovery;
- bounded queue saturation returns a typed error;
- cancellation and disposal stop reader/writer tasks without leaked tasks;
- reconnect resets client sequence, reauthenticates, and sends
  `last_seen_revision`;
- uncertain gameplay commands are not replayed after reconnect;
- text, oversized, malformed, and wrong-version frames fault the session;
- player tickets and private payloads never appear in diagnostic logs.

Add a real-gateway integration test only as an explicitly enabled test that
uses environment-provided endpoint, ticket, and room ID. Normal test runs must
not depend on the VPS or mutate a production room.

## Definition of done

- The package compiles for the selected Unity/.NET Standard target.
- Runtime and test asmdefs are present and generated code is isolated.
- Unit tests cover the state machine, ordering, backpressure, recovery, and
  security-sensitive logging behavior.
- Public async methods and failure modes have XML documentation.
- A README contains one minimal connect/auth/join/send example using
  `wss://api.ruleshift.ru/v2/ws` without embedding any secret.
- No game-specific mechanics, Developer API logic, unbounded queues, or Unity
  main-thread blocking were introduced.
