# Game jam promotions

`cmd/gamejam-promotions` is a standalone service that discovers Russian and Russian-language game jams, sends them through manual moderation, and verifies shared 10-digit promotion codes. It does not import the gateway, Developer API, control database, or module databases.

## Discovery and moderation

The service checks GameDev Afisha, Jammer, and itch.io on startup and every six hours. Source access is HTTPS-only, bounded, and subject to each site's `robots.txt`. A source failure is recorded without deleting or activating existing data.

Candidates are classified as `likely_ru`, `unknown`, or `unlikely_ru`. Classification never creates a promotion. An administrator opens `/admin/`, corrects normalized fields, chooses `venue_ru`, `language_ru`, `audience_ru`, or `organizer_ru`, and approves or rejects the candidate. Candidates from multiple sources can be merged into one approved game jam.

An identical nonempty official URL is treated as an exact cross-source duplicate and cannot be approved as a second game jam; the moderation UI offers the existing game jam as the merge target. Matching normalized title, dates, and organizer produces a non-blocking possible-duplicate suggestion. Later source changes are displayed as a field-level diff and never overwrite the approved snapshot until a moderator applies the update; the moderator can instead keep the snapshot and acknowledge the diff.

The admin listener is intended for the TLS hostname `admin.ruleshift.ru`. Every path, including metrics and health, requires HTTP Basic authentication. The password is configured as a bcrypt hash.

## Codes and public verification

Approval creates one cryptographically random 10-digit code. Leading zeroes are significant. Codes are looked up through HMAC-SHA256 and are stored encrypted with AES-256-GCM; both keys are derived from `GAMEJAM_CODE_MASTER_KEY`. The master key must be kept stable and backed up with PostgreSQL.

`POST /v1/gamejam-discounts/verify` accepts `{"code":"0123456789"}`. A code is valid only while its game jam is approved and the current Moscow calendar date is inside the inclusive event date range. Codes are shared, do not get consumed, and no use count is stored. Inactive and unknown codes return the same public result.

The full contract is [gamejam-promotions.openapi.yaml](../api/gamejam-promotions.openapi.yaml). The service never logs submitted codes, source URLs, game jam IDs, or client IP addresses.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `GAMEJAM_PUBLIC_ADDR` | `:8082` | Public verification listener. |
| `GAMEJAM_ADMIN_ADDR` | `:9092` | Basic-authenticated administration listener. |
| `GAMEJAM_DATABASE_URL` | required | Dedicated `ruleshift_gamejam` PostgreSQL database. |
| `GAMEJAM_CODE_MASTER_KEY` | required | Base64 encoding of exactly 32 stable random bytes. |
| `GAMEJAM_ADMIN_USERNAME` | required | Basic Auth username. |
| `GAMEJAM_ADMIN_PASSWORD_BCRYPT` | required | Bcrypt password hash. |
| `GAMEJAM_ALLOWED_ORIGIN` | `https://ruleshift.ru` | Exact custom-domain CORS origin for the GitHub Pages site. |
| `GAMEJAM_SYNC_INTERVAL` | `6h` | Scheduled discovery cadence. |
| `GAMEJAM_SOURCE_USER_AGENT` | Ruleshift bot identifier | Source contact identity. |
| `GAMEJAM_AFISHA_URL` | `https://gamedev-afisha.ru/` | Override for the Afisha source. |
| `GAMEJAM_JAMMER_URL` | `https://jammer.website/ru/jams` | Override for the Jammer source. |
| `GAMEJAM_ITCH_URL` | `https://itch.io/jams/upcoming` | Override for the itch.io source. |

Production manifests and secret/Ingress templates are under `deploy/k3s/gamejam-promotions`. The Ruleshift site requires `VITE_GAMEJAM_API_URL` at production build time.
