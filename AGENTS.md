# AGENTS.md

## Project

Ruleshift is a Go authoritative multiplayer state server for a future Steam-compatible Unity C# card game.

The MVP is not a card game. The MVP is a coherent protobuf-based shared integer state server.

## MVP

Clients connect to the Go gateway, authenticate, join a room, and send protobuf commands to modify one shared `int64` value.

The server is authoritative:
- clients send commands;
- the server applies commands sequentially;
- the server increments room revision;
- the server broadcasts protobuf state deltas or snapshots;
- all clients in a room must observe the same ordered revision stream.

## Development Rules

- Always inspect the repository before editing.
- Prefer small, reviewable changes.
- Use protobuf for client-server protocol.
- Do not use FlatBuffers in MVP.
- Do not implement card game mechanics in MVP.
- Keep room logic independent from network and Steam auth.
- Never trust client-side state.
- Keep all queues bounded.
- Do not block room runtimes on network writes.
- Use `context.Context` for long-running operations.
- Run `go test ./...` after meaningful backend changes.
- Update docs when architecture, commands, protocol, or config changes.

## Architecture Priorities

1. Authoritative server.
2. Protobuf protocol.
3. Coherent replicated integer state.
4. Sequential room runtime.
5. Reconnect and snapshot recovery.
6. Observability and load testing.
7. Future extensibility for card-game state.

## Hot Path Rules

- Avoid reflection-heavy work outside protobuf requirements.
- Avoid unbounded allocations.
- Avoid unbounded goroutines.
- Add benchmarks for protobuf codec, room command apply, broadcast, and gateway decode.

## Package Boundaries

- `internal/auth` owns identity providers and Steam-compatible auth.
- `internal/room` owns authoritative state and command ordering.
- `internal/protocol` owns protobuf schema, framing, and validation.
- `internal/net` owns transport/session plumbing.
- `cmd/gateway` wires packages together but should not contain domain logic.

## Documentation Rules

README must stay useful for an interviewer:
- What the project is.
- What the MVP demonstrates.
- How to run it.
- How protobuf messages flow.
- Why this demonstrates high-performance systems.
- What is implemented.
- What remains future work.

