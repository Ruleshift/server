# Ruleshift OCI module examples

Each directory is an independently buildable OCI image. Build from the server
repository root so the shared ABI implementation is available:

```bash
docker build -f examples/modules/hiddennumber/Dockerfile -t localhost:5000/hiddennumber:1.0.0 .
docker push localhost:5000/hiddennumber:1.0.0
docker inspect --format='{{index .RepoDigests 0}}' localhost:5000/hiddennumber:1.0.0
```

From PowerShell, the complete build, push, Developer API publication and
validation workflow can be run with:

```powershell
$env:RULESHIFT_DEVELOPER_API_KEY = "<developer-api-key>"
# Optional when Docker login and the Ruleshift pull credential need refreshing:
$env:GHCR_USERNAME = "<github-user>"
$env:GHCR_TOKEN = "<registry-token>"

.\scripts\publish-module.ps1 `
  -ModuleId hiddennumber `
  -DisplayName "Hidden Number" `
  -RegistryRepository ghcr.io/ruleshift/hiddennumber `
  -RegistryCredential main
```

Use `-PublicImage` for a public OCI package. The script refuses to overwrite an
existing SemVer, extracts the descriptor from the built image, publishes only
the resulting digest, and requires both the version and validation result to
be `active`.

Publish the resulting `name@sha256:...` reference, never the tag. These examples
share a small Go host only to keep this repository compact; they are separate
gRPC services and no Ruleshift core package imports them.

- `xiangqi`: two-player board-state and public projections;
- `hiddennumber`: player-private, public, and trusted full projections;
- `cardgame`: lobby lifecycle, deterministic deck, private hands, public hand
  counts, trusted full view, play/end-turn/modifier commands.
