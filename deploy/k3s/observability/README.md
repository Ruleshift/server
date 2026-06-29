# Ruleshift observability on k3s

These manifests target the existing single-node `ruleshift-core` namespace and
its `ruleshift-gateway` deployment. They create private ClusterIP services,
three local-path PVCs, Prometheus, Loki, Alloy, Grafana, and
`observability-api`.

The normal deployment path is `.github/workflows/deploy.yml`; no repository is
required on the VPS. See `docs/observability.md` for the one-time secrets and
GitHub Actions settings.

To render locally:

```sh
kubectl kustomize deploy/k3s/observability \
  --load-restrictor=LoadRestrictionsNone > ruleshift-observability.yaml
```

Do not add `ingress.example.yaml` to `kustomization.yaml`. It is intentionally
applied only after real DNS names and TLS secrets exist.
