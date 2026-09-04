# Ruleshift gateway on k3s

This package is the reproducible base installation for the authoritative
gateway. It creates the `ruleshift-core` namespace, scheduler RBAC, one gateway
pod, its ClusterIP Service, the public TLS Ingress, and an ingress-only
NetworkPolicy. PostgreSQL and real Secret values are deliberately external.

The one-replica Deployment uses `Recreate`: two gateway replicas must not serve
the same authoritative room concurrently. Room restore from PostgreSQL handles
the restart boundary.

## Prerequisites

- k3s with Traefik and NetworkPolicy enforcement;
- cert-manager with a `letsencrypt-prod` ClusterIssuer;
- `api.ruleshift.ru` resolving to the VPS;
- PostgreSQL reachable from the gateway, with a role allowed to create the
  control and per-module databases;
- node-level DNS and HTTPS access to the gateway and module OCI registries.

Only ports 80/443 should expose Ruleshift publicly. PostgreSQL, module gRPC
50051, and the operations listener 9091 remain private.

## 1. Select an immutable gateway image

Replace the all-zero digest in `deployment.yaml` with either:

```text
ghcr.io/ruleshift/server:<40-character-lowercase-git-sha>
ghcr.io/ruleshift/server@sha256:<64-lowercase-hex-digest>
```

If the package is private, create a pull Secret and attach it to the Service
Account before applying the Deployment:

```sh
kubectl apply -f deploy/k3s/gateway/namespace.yaml
kubectl apply -f deploy/k3s/gateway/service-account-rbac.yaml
kubectl -n ruleshift-core create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io \
  --docker-username="$GHCR_USERNAME" \
  --docker-password="$GHCR_TOKEN"
kubectl -n ruleshift-core patch serviceaccount ruleshift-gateway \
  --type=merge -p '{"imagePullSecrets":[{"name":"ghcr-pull"}]}'
```

The package must remain public if no pull Secret is configured.

## 2. Create runtime secrets

Do not commit a populated Secret. Create it directly from a protected shell or
secret manager. The Developer API key must contain at least 16 characters and
the public room reference key at least 32 stable random bytes.

```sh
kubectl apply -f deploy/k3s/gateway/namespace.yaml
kubectl -n ruleshift-core create secret generic ruleshift-gateway-secrets \
  --from-literal=RULESHIFT_DATABASE_URL="$RULESHIFT_DATABASE_URL" \
  --from-literal=RULESHIFT_DATABASE_ADMIN_URL="$RULESHIFT_DATABASE_ADMIN_URL" \
  --from-literal=RULESHIFT_DEVELOPER_API_KEY="$RULESHIFT_DEVELOPER_API_KEY" \
  --from-literal=RULESHIFT_PUBLIC_ROOM_REF_KEY="$RULESHIFT_PUBLIC_ROOM_REF_KEY"
```

`secret.example.yaml` documents the required names but is intentionally not
part of the Kustomization.

## 3. Render and apply

```sh
kubectl kustomize deploy/k3s/gateway
kubectl apply --dry-run=client -k deploy/k3s/gateway
kubectl apply -k deploy/k3s/gateway
kubectl -n ruleshift-core rollout status deployment/ruleshift-gateway --timeout=300s
```

## 4. Verify

```sh
kubectl get namespace ruleshift-core --show-labels
kubectl -n ruleshift-core get deployment,service,ingress,pods
kubectl auth can-i create namespaces \
  --as=system:serviceaccount:ruleshift-core:ruleshift-gateway
curl --fail --silent --show-error https://api.ruleshift.ru/healthz
```

The current `/healthz` and `/readyz` handlers are process-only checks. Complete
verification also publishes an example module, creates a room, and joins it
through the binary protobuf WebSocket at `wss://api.ruleshift.ru/v2/ws`.

The optional `deploy/k3s/observability` package supplies
`ruleshift-gateway-observability`, changes the operations bind address to
`:9091`, and creates the private `ruleshift-operations` Service.
