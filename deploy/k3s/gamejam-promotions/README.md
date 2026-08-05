# Game jam promotions deployment

This directory deploys the standalone game jam discovery and promotion-code service. It never uses the Ruleshift control or module databases.

1. Create a dedicated `ruleshift_gamejam` PostgreSQL database.
2. Copy `secret.example.yaml`, replace every placeholder, and apply it separately.
3. Apply `kustomization.yaml`.
4. Replace DNS/TLS values in `ingress.example.yaml` and apply it separately.
5. Set the GitHub repository variable `GAMEJAM_API_URL=https://user.ruleshift.ru` before publishing the site.

The admin hostname is internet-accessible by design but every path is protected by application Basic Auth and must be served only through TLS. The code master key must remain stable across rollouts and be backed up with PostgreSQL.
