# Client Packaging

The Ruleshift console client can be distributed as a single Windows `.exe`.

Build a ready-to-share package:

```powershell
.\scripts\package-console.ps1
```

The package is written to:

```text
dist\ruleshift-console-windows-amd64\
dist\ruleshift-console-windows-amd64.zip
```

It contains:

- `ruleshift-console.exe`
- `player-1.cmd`
- `player-2.cmd`
- `README.txt`

Run the package by double-clicking `player-1.cmd` and `player-2.cmd`, or by running:

```powershell
.\ruleshift-console.exe -addr ws://147.45.211.122:8080/ws -ticket mock:player-1 -room demo
```

## What The Client Needs

The console client is built from:

- `cmd/console`
- `internal/protocol`
- `internal/protocol/generated/go/ruleshiftv1`
- third-party protobuf and WebSocket libraries from `go.mod`

The compiled `.exe` includes those dependencies. A player does not need the source tree, Go, protobuf tooling, or the server packages.

## What The Server Needs

The server image is built from:

- `cmd/gateway`
- `internal/gateway`
- `internal/room`
- `internal/auth`
- `internal/config`
- `internal/protocol`
- supporting internal packages used by the gateway

The VPS only needs Docker and the published container image.

## Should Client And Server Be Split?

For the MVP, keep them in one repository. The shared protobuf schema and generated protocol code are changing together, so one repo keeps the wire contract easy to review.

A good future split is:

- `ruleshift-protocol`: `.proto` files and generated client bindings.
- `ruleshift-server`: Go gateway, room runtime, auth, allocator, matchmaking.
- `ruleshift-tools`: CLI and debugging clients, or a proper Unity sample client.

Split when external users need the client SDK independently, or when server-only code starts making the client distribution confusing.
