# Performance Report

This file is a measurement template for later phases.

## Target MVP Scenario

- 1,000 simulated players locally.
- 100 active rooms.
- WebSocket binary protobuf traffic.
- Bounded room queues and bounded session send queues.
- No data races under `go test -race ./...`.

## Commands

```powershell
go test ./...
go test -race ./...
go test -bench ./...
go run ./cmd/botload -players 100 -rooms 10 -duration 30s
```

## Planned Metrics

Counters:

- `active_connections`
- `active_rooms`
- `commands_received_total`
- `commands_rejected_total`
- `deltas_sent_total`
- `snapshots_sent_total`
- `reconnects_total`
- `slow_consumers_total`

Histograms:

- `command_processing_duration`
- `end_to_end_command_to_delta_duration`
- `protobuf_encode_duration`
- `protobuf_decode_duration`

## Current Results

No network load results yet. Phase 1 only establishes the compileable project skeleton.


