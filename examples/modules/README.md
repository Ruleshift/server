# Ruleshift OCI module examples

Each directory is an independently buildable OCI image. Build from the server
repository root so the shared ABI implementation is available:

```bash
docker build -f examples/modules/hiddennumber/Dockerfile -t localhost:5000/hiddennumber:1.0.0 .
docker push localhost:5000/hiddennumber:1.0.0
docker inspect --format='{{index .RepoDigests 0}}' localhost:5000/hiddennumber:1.0.0
```

Publish the resulting `name@sha256:...` reference, never the tag. These examples
share a small Go host only to keep this repository compact; they are separate
gRPC services and no Ruleshift core package imports them.

- `xiangqi`: two-player board-state and public projections;
- `hiddennumber`: player-private, public, and trusted full projections;
- `cardgame`: lobby lifecycle, deterministic deck, private hands, public hand
  counts, trusted full view, play/end-turn/modifier commands.
