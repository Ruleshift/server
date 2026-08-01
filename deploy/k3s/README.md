# Ruleshift k3s operations

## Updating the gateway

`scripts/update-gateway.sh` updates the gateway Deployment from an immutable
Git SHA tag or OCI digest. It verifies the current gateway, waits for the new
rollout, checks the pod-local and public health endpoints, and restores the
previous image when an update fails.

Copy it from the workstation; the VPS does not need a Git checkout:

```powershell
scp scripts/update-gateway.sh root@147.45.211.122:/tmp/ruleshift-update-gateway
```

Install it once on the VPS:

```bash
sudo install -o root -g root -m 0750 \
  /tmp/ruleshift-update-gateway \
  /usr/local/sbin/ruleshift-update-gateway
rm /tmp/ruleshift-update-gateway
```

Run an update after the GitHub Packages image has been published:

```bash
sudo /usr/local/sbin/ruleshift-update-gateway \
  ghcr.io/ruleshift/server:<40-character-git-sha>
```

For stronger registry immutability, an OCI digest is also accepted:

```bash
sudo /usr/local/sbin/ruleshift-update-gateway \
  ghcr.io/ruleshift/server@sha256:<64-hex-digest>
```

The defaults target `ruleshift-core/ruleshift-gateway` and check
`https://api.ruleshift.ru/healthz`. Use environment variables only when the
deployment differs:

```bash
EXPECTED_CONTEXT=default PUBLIC_HEALTH_URL=https://api.ruleshift.ru/healthz \
  sudo --preserve-env=EXPECTED_CONTEXT,PUBLIC_HEALTH_URL \
  /usr/local/sbin/ruleshift-update-gateway ghcr.io/ruleshift/server:<git-sha>
```

If the old gateway is already unhealthy, explicitly skip only the preflight
health check; post-update checks and automatic rollback remain enabled:

```bash
SKIP_PREFLIGHT_HEALTH=1 sudo --preserve-env=SKIP_PREFLIGHT_HEALTH \
  /usr/local/sbin/ruleshift-update-gateway ghcr.io/ruleshift/server:<git-sha>
```

## Purging module versions

`scripts/purge-module-version.sh` removes selected module-version Kubernetes
resources and database records. It is intentionally dry-run by default and
requires `--yes` before deleting anything.

Copy it from the workstation:

```powershell
scp scripts/purge-module-version.sh root@147.45.211.122:/tmp/ruleshift-purge-module-version
```

Install it once on the VPS:

```bash
sudo install -o root -g root -m 0750 \
  /tmp/ruleshift-purge-module-version \
  /usr/local/sbin/ruleshift-purge-module-version
rm /tmp/ruleshift-purge-module-version
```

Preview a failed module version purge:

```bash
sudo /usr/local/sbin/ruleshift-purge-module-version \
  --developer-id default \
  --module-id leastpopular \
  --version 1.0.3 \
  --delete-rooms
```

Execute it:

```bash
sudo /usr/local/sbin/ruleshift-purge-module-version \
  --developer-id default \
  --module-id leastpopular \
  --version 1.0.3 \
  --delete-rooms \
  --yes
```

By default the script only selects `failed` versions. Purging active or inactive
versions is destructive for rooms pinned to those versions and must be requested
explicitly with `--status any --allow-non-failed --delete-rooms --yes`.
