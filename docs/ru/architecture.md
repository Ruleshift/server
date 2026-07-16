# Архитектура

Ruleshift — authoritative-сервис многопользовательского состояния. Правила игр
реализуются внешними stateless OCI-модулями; core не зависит от конкретной игры.

```mermaid
flowchart LR
    Player["Player SDK"] -->|"protobuf v2 / WebSocket"| Gateway
    Backend["Trusted backend / Developer SDK"] -->|"HTTP v2"| Control
    Gateway --> Registry["Реестр комнат"]
    Registry --> Queue["Ограниченная последовательная очередь комнаты"]
    Queue -->|"gRPC ABI v1 + текущее состояние"| Module["Workload закреплённого OCI-модуля"]
    Module -->|"новое состояние + delta/view"| Queue
    Queue --> Store["БД модуля: события + snapshots"]
    Control --> ControlDB["Control DB: модули + версии + маршруты"]
    Control --> Scheduler["Kubernetes scheduler"]
    Scheduler --> Module
```

## Границы пакетов

- `internal/module`: opaque state, ссылка на модуль, ABI client и лимиты;
- `internal/roomcore`: владение состоянием, упорядоченный поток ревизий и replay;
- `internal/gatewayv2`: только player transport;
- `internal/controlplane`: публикация, validation и lifecycle активации;
- `internal/scheduler/kubernetes`: изоляция tenants и workloads модулей;
- `internal/storage/postgres`: control- и module-persistence;
- `examples/modules`: независимо собираемые OCI-сервисы, которые core никогда не импортирует.

В core нет встроенных игровых reducers. Hidden Number, Xiangqi и Card Game
находятся в `examples/modules` и собираются как отдельные OCI-сервисы.

Общая инфраструктура делегируется поддерживаемым библиотекам, а authoritative
инварианты остаются в проекте. Граница и принятые зависимости описаны в
[политике зависимостей](dependency-policy.md).

## Инварианты версий и комнат

Маршрут комнаты закрепляет комбинацию
`developer_id + module_id + version + image_digest`. Новые комнаты используют
active version; существующие комнаты никогда не переключаются автоматически.
Degraded active version исключается из создания комнат, а fallback выбирается
из последней healthy inactive version.

Room runtime меняет состояние в памяти только после успешного вызова модуля,
успешных projections и persistence commit. Событие, ревизия состояния и
периодический snapshot записываются одной транзакцией в БД модуля. Snapshots
сохраняются на ревизии 0, каждые 100 ревизий и при graceful eviction.

## Изоляция Kubernetes

Каждый разработчик получает namespace со стабильным hash, ResourceQuota,
LimitRange и default-deny правилами ingress/egress. В MVP каждая validated version
получает Deployment и ClusterIP Service без явно заданной политики количества
реплик; Kubernetes применяет значение по умолчанию. HA-политика реплик отложена
до этапа после MVP. Pods запускаются не от root, с read-only root filesystem,
seccomp `RuntimeDefault`, без capabilities,
privilege escalation, service-account token, host mounts и external egress.

Registry credentials существуют только как tenant-scoped
`dockerconfigjson` Secrets. В кластере должно быть включено encryption-at-rest
для Secrets.

## Модель ошибок

- отказ модуля принять команду: стабильная ошибка `command_rejected`, ревизия не меняется;
- timeout или недоступность: `module_unavailable`, ревизия не меняется;
- malformed, oversized или wrong-type response: protocol violation;
- три нарушения за 60 секунд: версия становится degraded;
- рестарт gateway: загружается route, точная версия модуля, последний snapshot,
  затем проигрываются последующие generic lifecycle/command events.

Все очереди ограничены, обработка комнаты последовательна, а network writers
никогда не владеют authoritative state комнаты.
