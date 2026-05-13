# Bot Load Scenarios

`cmd/botload` is currently a CLI skeleton. Later phases will add WebSocket clients that authenticate, join rooms, send integer commands, reconnect, and verify coherence.

Planned smoke test:

```powershell
go run ./cmd/botload -players 100 -rooms 10 -duration 30s
```

Planned high-load local target:

```powershell
go run ./cmd/botload -players 1000 -rooms 100 -duration 60s -rps 5000
```

Coherence checks:

- revisions are monotonic per room;
- clients in the same room converge to the same value;
- snapshots recover clients that missed deltas.


