# Card Game as a protocol v2 module

Card Game no longer runs inside Ruleshift core. Its external OCI example is in
`examples/modules/cardgame` and implements Module Runtime ABI v1.

The image supports:

- a host-controlled lobby for 2–6 players;
- deterministic deck creation from the Ruleshift room seed;
- private hands for player scope;
- public hand counts for spectators;
- complete hands for trusted full scope;
- `start`, `play_card`, `attach_modifier`, and `end_turn` commands;
- player join/leave lifecycle and deterministic conformance vectors.

Build it from the repository root:

```powershell
docker build -f examples/modules/cardgame/Dockerfile `
  -t registry.example.com/cardgame:1.0.0 .
docker push registry.example.com/cardgame:1.0.0
```

Publish the resulting `registry.example.com/cardgame@sha256:...` together with:

- `examples/modules/cardgame/manifest.json`;
- the generated `descriptor.pb`;
- `examples/modules/cardgame/conformance.json`.

The module-specific `Command` protobuf contains canonical JSON in field 1 in
this compact example. Commands use objects such as:

```json
{"kind":"start"}
{"kind":"play_card","card_id":"card-1-0"}
{"kind":"attach_modifier","card_id":"modifier-id","target_card_id":"target-id"}
{"kind":"end_turn"}
```

Production games can replace this envelope with strongly typed protobuf fields
without changing the Ruleshift ABI or core.
