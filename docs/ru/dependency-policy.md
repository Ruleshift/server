# Политика зависимостей

Ruleshift использует зрелые библиотеки для общей инфраструктуры и оставляет
самописный код только там, где он выражает инварианты authoritative-сервиса.

## Инфраструктура на готовых библиотеках

- разбор переменных окружения: `github.com/caarlos0/env/v11`;
- декларативная проверка конфигурации: `github.com/go-playground/validator/v10`;
- подпись connect-токенов и стандартные time claims: `github.com/golang-jwt/jwt/v5`;
- строгий разбор SemVer: `github.com/Masterminds/semver/v3`;
- разбор OCI image reference: `github.com/distribution/reference`;
- идентификаторы комнат, tickets, matches, assignments, reservations и
  operations: `github.com/google/uuid`;
- равномерная генерация invite-кодов: `github.com/matoous/go-nanoid/v2`;
- запросы к Prometheus и декодирование ответов: официальный клиент
  `github.com/prometheus/client_golang/api/prometheus/v1`;
- PostgreSQL connection pools, транзакции, сбор строк, native numeric codecs и
  quoting идентификаторов: `github.com/jackc/pgx/v5` и `pgxpool`;
- PostgreSQL SQLSTATE constants: `github.com/jackc/pgerrcode`;
- HTTP, JSON, multipart, authentication и bounded responses в developer SDK:
  `github.com/go-resty/resty/v2`;
- polling и создание указателей для Kubernetes:
  `k8s.io/apimachinery/pkg/util/wait` и `k8s.io/utils/ptr`;
- проверка и поиск protobuf descriptors: `protodesc` и `protoreflect`.

Для копирования, сортировки и числовых операций используются стандартные
`slices`, `cmp` и `math`, а не локальные helpers.

## Код, который намеренно остаётся в проекте

Следующие части выражают доменную семантику, а не общую инфраструктуру. Их не
следует заменять фреймворком, пока он не сохраняет все текущие инварианты:

- `internal/roomcore`: одна bounded sequential queue владеет потоком ревизий
  комнаты, а commit выполняется только после module projection и persistence;
- `internal/matchmaking` и `internal/allocator`: lifecycle, idempotency, учёт
  мест и bounded in-memory состояние MVP;
- PostgreSQL migration runner: embedded platform migrations, динамически
  скомпилированные module migrations, версии компонентов, checksums,
  транзакции и advisory locks;
- публичные ссылки комнат: необратимая HMAC-проекция приватного room ID;
- лимиты payload и семантика API errors: проект сохраняет собственные границы
  и контракты, а transport mechanics делегирует поддерживаемым клиентам.

Для новой зависимости предпочтительны поддерживаемый стабильный модуль,
узкий API, совместимая лицензия, поддержка context и явно ограниченное
поведение без скрытых goroutines и retries. В authoritative hot path библиотека
допускается только при прозрачных allocation и concurrency semantics.
