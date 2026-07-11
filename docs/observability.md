# Observability MVP

Ruleshift monitoring has three deliberately separate surfaces:

- GitHub Pages is the public product overview and room browser.
- `observability-api` serves only aggregate health and safe room projections.
- Grafana renders metrics and cleaned logs from private Prometheus and Loki.

## Data flow

```text
Ruleshift /metrics  -> Prometheus
Ruleshift JSON logs -> Alloy -> Loki
Pages               -> observability-api -> Prometheus + private operations API
Grafana             -> private Prometheus + Loki
```

Tempo, tracing, public log APIs, alert notifications, and Grafana Infinity are
outside the MVP.

## Gateway configuration

The public gateway listener never serves metrics or diagnostics. Configure the
private listener separately:

```text
RULESHIFT_OPERATIONS_ADDR=:9091
RULESHIFT_PUBLIC_ROOM_REF_KEY=<at least 32 random bytes>
RULESHIFT_QUEUE_DEGRADED_RATIO=0.8
RULESHIFT_ENABLE_METRICS=true
```

Do not publish port `9091`. Prometheus and `observability-api` reach it through
the private Docker/Kubernetes network.

`public_room_ref` is `rm_` plus a 128-bit HMAC-SHA256 digest encoded as lowercase
Base32. Keep the key stable to preserve links across restarts. The public room
contract contains revision, module version, timestamps, connection count, and
queue usage only.

## k3s deployment on the VPS

Production uses the manifests in `deploy/k3s/observability`. The VPS does not
need a Git checkout or Docker Compose. GitHub Actions builds immutable GHCR
images, renders Kustomize locally, sends the rendered YAML over SSH, and asks
k3s to roll out the exact commit SHA.

Create the two Kubernetes secrets once on the VPS. Neither value belongs in Git:

```sh
k3s kubectl -n ruleshift-core create secret generic ruleshift-observability-secret \
  --from-literal=RULESHIFT_PUBLIC_ROOM_REF_KEY="$(openssl rand -hex 32)"

k3s kubectl -n ruleshift-core create secret generic ruleshift-grafana-admin \
  --from-literal=password="$(openssl rand -base64 32)"
```

Configure these GitHub Actions secrets:

```text
SERVER_DNS_NAME=<VPS DNS name>
SERVER_LOGIN=<VPS SSH login>
SERVER_PASSWORD=<VPS SSH password>
```

Add the repository variable `K3S_AUTO_DEPLOY=true`. The VPS SSH server must
allow password authentication for `SERVER_LOGIN`. The workflow records the
host key with `ssh-keyscan`, authenticates non-interactively through
`SSH_ASKPASS`, and keeps strict host-key checking enabled for subsequent SSH
commands in the job. The new
`ghcr.io/ruleshift/server-observability` package must be public, like the
gateway package, or the namespace must have a GHCR `imagePullSecret`.

For the first release, leave `K3S_AUTO_DEPLOY` disabled, push once so GHCR
creates the observability package, make that package public, enable the
variable, and start the workflow again with **Run workflow**.

Every push to `main` now tests the code, publishes both images, applies the
observability resources, updates the gateway environment, and rolls out the
gateway and API. k3s uses containerd, so there is no separate `docker pull`.
To deploy a known image manually:

```sh
k3s kubectl -n ruleshift-core set image deployment/ruleshift-gateway \
  gateway=ghcr.io/ruleshift/server:<commit-sha>
k3s kubectl -n ruleshift-core set image deployment/ruleshift-observability-api \
  observability-api=ghcr.io/ruleshift/server-observability:<commit-sha>
k3s kubectl -n ruleshift-core rollout status deployment/ruleshift-gateway
k3s kubectl -n ruleshift-core rollout status deployment/ruleshift-observability-api
```

Prometheus, Loki, the gateway operations listener, and the API remain
`ClusterIP` services. PostgreSQL is unchanged. Before configuring public DNS,
smoke-test through port forwarding:

```sh
k3s kubectl -n ruleshift-core get pods
k3s kubectl -n ruleshift-core port-forward service/ruleshift-grafana 3000:3000
k3s kubectl -n ruleshift-core port-forward service/ruleshift-observability-api 8081:8081
```

Public GitHub Pages access requires HTTPS ingress only for
`ruleshift-observability-api` and Grafana. Keep Prometheus, Loki, PostgreSQL,
`ruleshift-operations`, and the gateway metrics endpoint internal. An ingress
template is provided at `deploy/k3s/observability/ingress.example.yaml`; replace
its hosts and TLS secret names before applying it separately.

Docker Compose in `deploy/observability/compose.yaml` is retained only for
local development.

Grafana provisions two dashboards:

- `Ruleshift System Overview`
- `Ruleshift Runtime Diagnostics`

Anonymous access stays disabled. After deployment, share each dashboard through
Grafana's **Share externally** action and place the resulting URLs in
`OBS_GRAFANA_SYSTEM_URL` and `OBS_GRAFANA_RUNTIME_URL`.

## Public API

```text
GET /healthz
GET /v1/overview
GET /v1/rooms?cursor=&limit=&status=&module=&q=
GET /v1/rooms/{public_room_ref}
```

`observability-api` never accepts PromQL. Overview uses fixed queries and these
configurable thresholds:

```text
OBS_STALE_AFTER=30s
OBS_UNAVAILABLE_AFTER=60s
OBS_ERROR_RATIO_THRESHOLD=0.01
OBS_COMMAND_P95_THRESHOLD=250ms
OBS_QUEUE_SATURATION_THRESHOLD=0.8
OBS_SLOW_CONSUMER_RATIO_THRESHOLD=0.005
OBS_MIN_COMMANDS=100
```

Set `OBS_ALLOWED_ORIGIN` to the exact GitHub Pages origin. The API forwards only
bounded room filters and caps pages at 100 items.

## Label and logging policy

Prometheus and Loki labels are limited to bounded values such as component,
operation, result, reason, message type, direction, environment, and log level.
Never label by room, public room reference, player, developer, module version, or
trace ID.

Application logs are JSON. Do not log authorization headers, API keys, Steam
tickets, player identities, protobuf payloads, authoritative state, or database
URLs. Internal room IDs must not reach public Loki dashboards.
