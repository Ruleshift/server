# CLI Client

`cmd/client` is a small terminal client for end-to-end MVP checks without Unity.

It uses the same production path as a game client:

```text
WebSocket binary message -> protobuf ClientEnvelope -> gateway -> room runtime -> protobuf ServerEnvelope
```

## Start The Server

On the server machine:

```bash
RULESHIFT_ADDR=0.0.0.0:8080 go run ./cmd/gateway
```

On Windows PowerShell:

```powershell
$env:RULESHIFT_ADDR="0.0.0.0:8080"
go run ./cmd/gateway
```

Health check:

```powershell
Invoke-WebRequest http://192.168.1.50:8080/healthz
```

## Commands

Read current state:

```powershell
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-1 -room demo -op get
```

Add to the shared integer:

```powershell
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-1 -room demo -op add -value 5
```

Set the shared integer:

```powershell
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-2 -room demo -op set -value 42
```

Watch snapshots and deltas:

```powershell
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:watcher -room demo -op watch
```

Stop watch mode with `Ctrl+C`.

## Two Windows Clients

Open watcher on client A:

```powershell
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-1 -room demo -op watch
```

Send commands from client B:

```powershell
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-2 -room demo -op add -value 10
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-2 -room demo -op set -value 100
```

Client A should print the same ordered `revision` stream produced by the server.

## Interactive Console

For repeated manual checks, use the interactive console instead of retyping the long command line.

Build it once:

```powershell
go build -o ruleshift-console.exe ./cmd/console
```

Open client A:

```powershell
.\ruleshift-console.exe -addr ws://147.45.211.122:8080/ws -ticket mock:player-1 -room demo
```

Open client B in another terminal:

```powershell
.\ruleshift-console.exe -addr ws://147.45.211.122:8080/ws -ticket mock:player-2 -room demo
```

Inside either console, use short commands:

```text
get
add 10
set 42
room demo-2
status
quit
```

Both clients stay connected and print snapshots or deltas from the room as the server broadcasts them.

## Revision Checks

By default, Add/Set uses `expected_revision=0`, which means a blind authoritative update accepted by the server.

Use strict revision checking when you want the command to be rejected if the room changed after join:

```powershell
go run ./cmd/client -addr ws://192.168.1.50:8080/ws -ticket mock:player-1 -room demo -op add -value 1 -strict-revision
```
