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

## VPS stack

Create the shared private network, configure environment variables, and start
the stack:

```sh
docker network create ruleshift-private
docker compose -f deploy/observability/compose.yaml up -d --build
```

Attach the Ruleshift gateway container to `ruleshift-private` with the DNS name
`ruleshift`. The compose file binds Grafana and `observability-api` only to
localhost; terminate public HTTPS in the VPS reverse proxy. Prometheus and Loki
have no host port mappings.

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
