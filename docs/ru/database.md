# Архитектура базы данных

Ruleshift использует одну control PostgreSQL database и отдельную изолированную
database для каждой пары developer/module.

## Control database

- `developers` и хешированные `developer_api_keys`;
- `users` и identities провайдеров;
- `modules` и текущая active version;
- immutable `module_versions` с OCI- и descriptor-digests;
- ограниченные `module_validation_runs`;
- `room_routes`, закреплённые за developer/module/version/image digest и
  неизменяемым `player_count`;
- уникальные шестисимвольные `room_invite_codes` с deadline 24 часа.

Registry username, password и token здесь никогда не сохраняются.

## Module database

- `rooms`: revision, статус lobby/active, требуемое число игроков, seed, opaque
  protobuf state и SHA-256 digest;
- `room_participants`: устойчивое соответствие аутентифицированного игрока
  месту, отдельное от protobuf-состояния модуля;
- `room_events`: generic command event, protobuf input/delta bytes,
  диапазон ревизий и digest итогового состояния;
- `room_snapshots`: opaque protobuf state каждые 100 ревизий и при eviction;
- additive developer tables, объявленные в manifest версии.

В схеме нет игровых enums, reducer-specific event types, JSON command payloads
или миграций встроенных Go-модулей.

## Декларативные миграции

Manifest версии может добавлять таблицы со столбцами `string`, `int64`,
`float64`, `bool`, `timestamp` и `json`. Версии миграций положительны и
immutable. Ruleshift компилирует декларацию в SQL и проверяет checksums.

Модули не получают credentials базы данных и не могут выполнять запросы к ней.
Только trusted backends используют ограниченный Developer API
`CreateRow`/`ListRows`.

При создании комнаты route и код приглашения из `0-9A-Z` атомарно записываются
в control database. Deadline равен ровно 24 часам после `room_routes.created_at`.

## Локальный PostgreSQL

```powershell
docker compose up -d postgres
```

Для внешних Module Deployments gateway также требуется Kubernetes. Схема
является breaking pre-production change, поэтому сначала сбросьте старые
локальные volumes:

```powershell
docker compose down -v
```
