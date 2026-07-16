# Dependency policy

Ruleshift uses mature libraries for generic infrastructure and keeps custom
code only where it expresses an authoritative game-service invariant.

## Library-owned infrastructure

- environment decoding: `github.com/caarlos0/env/v11`;
- declarative configuration validation: `github.com/go-playground/validator/v10`;
- connect-token signing and registered time claims: `github.com/golang-jwt/jwt/v5`;
- strict semantic-version parsing: `github.com/Masterminds/semver/v3`;
- OCI image-reference parsing: `github.com/distribution/reference`;
- room, ticket, match, assignment, reservation, and operation identifiers:
  `github.com/google/uuid`;
- uniform invite-code generation: `github.com/matoous/go-nanoid/v2`;
- Prometheus HTTP queries and response decoding: the official
  `github.com/prometheus/client_golang/api/prometheus/v1` client;
- PostgreSQL connection pools, transactions, row collection, native numeric
  codecs, and identifier quoting: `github.com/jackc/pgx/v5` and `pgxpool`;
- PostgreSQL SQLSTATE constants: `github.com/jackc/pgerrcode`;
- developer SDK HTTP, JSON, multipart, authentication, and bounded response
  handling: `github.com/go-resty/resty/v2`;
- Kubernetes polling and pointer construction: `k8s.io/apimachinery/pkg/util/wait`
  and `k8s.io/utils/ptr`;
- protobuf descriptor validation and lookup: `protodesc` and `protoreflect`.

Standard-library `slices`, `cmp`, and `math` are preferred over local copying,
sorting, and numeric helpers.

## Intentionally project-owned code

The following components are not generic infrastructure and should not be
replaced with a framework unless its semantics match the existing invariants:

- `internal/roomcore`: one bounded sequential queue owns each room revision
  stream and commits only after module projection and persistence succeed;
- `internal/matchmaking` and `internal/allocator`: domain lifecycle,
  idempotency, seat accounting, and bounded in-memory MVP state;
- PostgreSQL migration runner: supports embedded platform migrations and
  dynamically compiled module migrations, per-component versions, checksums,
  transactions, and advisory locks;
- public room references: keyed, irreversible HMAC projections of private room
  IDs;
- payload limits and API error semantics: preserve Ruleshift-specific bounds
  and contracts while delegating transport mechanics to maintained clients.

When adding a dependency, prefer a maintained stable module with a narrow API,
compatible license, context support, bounded behavior, and no hidden goroutine
or retry policy. Keep libraries out of the authoritative hot path unless they
make allocation and concurrency behavior explicit.
