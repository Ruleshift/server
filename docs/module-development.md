# Создание внешнего модуля Ruleshift

Модуль Ruleshift — это stateless gRPC-сервис в OCI image. Репозиторий и язык
модуля выбирает разработчик игры. Клонировать Ruleshift и добавлять пакет в
`internal/game` больше не требуется.

Ruleshift хранит состояние комнаты, ревизии, event log и snapshots. Модуль
получает текущее protobuf-состояние и возвращает следующее. Клиент никогда не
присылает готовое состояние.

```text
player command -> Ruleshift room queue -> ModuleRuntime.Apply
                                      <- next_state + delta
               -> transactional event/snapshot -> projected broadcast
```

## 1. Что понадобится

- любой язык с gRPC и protobuf;
- Docker или другой OCI builder;
- registry, доступный Kubernetes-кластеру Ruleshift;
- Developer API key для Editor, CI или trusted backend;
- собственный module repository.

Developer API key нельзя включать в player build.

## 2. Подключите ABI

Скопируйте `internal/moduleruntime/proto/module_runtime.proto` в свой репозиторий
или подключите опубликованный ABI package. ABI v1 содержит сервис:

```proto
service ModuleRuntime {
  rpc Describe(DescribeRequest) returns (DescribeResponse);
  rpc NewState(NewStateRequest) returns (TransitionResponse);
  rpc PlayerJoined(PlayerTransitionRequest) returns (TransitionResponse);
  rpc PlayerLeft(PlayerTransitionRequest) returns (TransitionResponse);
  rpc Apply(ApplyRequest) returns (TransitionResponse);
  rpc ProjectSnapshot(ProjectRequest) returns (ProjectionResponse);
  rpc ProjectDelta(ProjectDeltaRequest) returns (ProjectionResponse);
}
```

Сгенерируйте bindings стандартными инструментами своего языка. Для Go:

```powershell
protoc -I . --go_out=. --go-grpc_out=. proto/module_runtime.proto
```

## 3. Опишите protobuf своей игры

Типы игры не добавляются в player protocol Ruleshift. Создайте отдельный proto:

```proto
syntax = "proto3";
package acme.mygame.v1;

message State {
  repeated Player players = 1;
  uint32 turn = 2;
}

message Player {
  string player_id = 1;
  int64 private_score = 2;
}

message PlayCommand { uint32 cell = 1; }

message Delta {
  string player_id = 1;
  uint32 cell = 2;
}

message View {
  repeated PublicPlayer players = 1;
  optional int64 own_private_score = 2;
}
```

Полные type URLs будут такими:

```text
type.googleapis.com/acme.mygame.v1.State
type.googleapis.com/acme.mygame.v1.PlayCommand
type.googleapis.com/acme.mygame.v1.Delta
type.googleapis.com/acme.mygame.v1.View
```

Пакеты `ruleshift.*` зарезервированы core. Descriptor set публикуется вместе с
версией:

```powershell
protoc -I . --include_imports --descriptor_set_out=descriptor.pb proto/mygame.proto
```

## 4. Реализуйте stateless runtime

Контейнер не хранит room state между RPC. Нельзя использовать global map вида
`room_id -> state`: Ruleshift может направить следующий запрос в другую replica.

Псевдокод `Apply`:

```go
func (s *Server) Apply(ctx context.Context, req *modulev1.ApplyRequest) (*modulev1.TransitionResponse, error) {
    current := unpackState(req.State)
    command := unpackPlayCommand(req.Command)

    if !current.IsPlayersTurn(req.PlayerId) {
        return nil, status.Error(codes.FailedPrecondition, "not player's turn")
    }

    next := current.Clone()
    delta, err := next.Play(req.PlayerId, command.Cell)
    if err != nil {
        return nil, status.Error(codes.InvalidArgument, err.Error())
    }

    return &modulev1.TransitionResponse{
        Changed:   true,
        NextState: pack(next),
        Delta:     pack(delta),
    }, nil
}
```

Обязательные свойства:

- одинаковый запрос даёт byte-for-byte одинаковый ответ;
- случайность берётся только из `RoomContext.seed`;
- время берётся только из `RoomContext.now_unix_ms`;
- `player_id` берётся из запроса Ruleshift и считается authenticated;
- внешние side effects запрещены;
- БД, filesystem mounts и внешний network модулю недоступны;
- `operation_id` сохранять не нужно: side effects всё равно запрещены.

Ошибка или timeout не изменяют состояние и revision комнаты. Ожидаемые ошибки
команды возвращайте через `InvalidArgument`, `FailedPrecondition` или
`PermissionDenied`. Ruleshift преобразует их в `command_rejected`.

## 5. Реализуйте projections

Canonical state никогда не отправляется игроку напрямую. `ProjectSnapshot` и
`ProjectDelta` получают `Viewer`:

| mode/scope | Назначение |
| --- | --- |
| player/player | приватный view конкретного игрока |
| spectator/public | публичная трансляция без секретов |
| spectator/full | trusted observer с полным view |

Не полагайтесь только на `player_id`: проверяйте scope. Для невидимого изменения
верните `no_visible_change = true` и валидный protobuf payload. Ruleshift считает
SHA-256 каждого view и помещает его в player protocol.

## 6. Лимиты и deadlines

| Payload | Максимум |
| --- | ---: |
| canonical state | 1 MiB |
| command | 256 KiB |
| delta | 256 KiB |
| projected view | 256 KiB |

Стандартный transition deadline — 50 ms, `NewState` — 250 ms. Manifest может
уменьшить deadline или поднять transition deadline до 250 ms. Ruleshift делает
не больше одного retry и только для gRPC `Unavailable`, внутри исходного
deadline.

## 7. Реализуйте Describe

`Describe` должен точно совпасть с manifest:

```go
return &modulev1.DescribeResponse{
    ModuleId:            "mygame",
    Version:             "1.0.0",
    AbiVersion:          1,
    StateTypeUrl:        "type.googleapis.com/acme.mygame.v1.State",
    CommandTypeUrls:     []string{"type.googleapis.com/acme.mygame.v1.PlayCommand"},
    DescriptorSetSha256: descriptorSHA256,
    SupportsPlayerLeft:  true,
}
```

Также зарегистрируйте стандартный gRPC health service. Readiness наступает только
после готовности обеих replicas и успешного `Describe`.

## 8. Создайте manifest

```json
{
  "module_id": "mygame",
  "version": "1.0.0",
  "abi_version": 1,
  "state_type_url": "type.googleapis.com/acme.mygame.v1.State",
  "command_type_urls": [
    "type.googleapis.com/acme.mygame.v1.PlayCommand"
  ],
  "transition_deadline_ms": 75,
  "capabilities": [
    "player_lifecycle",
    "private_projection",
    "public_projection"
  ]
}
```

Версия обязана быть SemVer. Повторная публикация того же SemVer и digest
идемпотентна. Другой digest с уже существующим SemVer отклоняется.

## 9. Optional additive database schema

Модуль не получает прямой доступ к БД. Если trusted backend нужны свои таблицы,
добавьте декларативные additive migrations в manifest:

```json
{
  "database_migrations": [
    {
      "version": 1,
      "name": "create_profiles",
      "tables": [
        {
          "name": "profiles",
          "columns": [
            {"name":"player_id","type":"string","primary_key":true},
            {"name":"rating","type":"int64"}
          ]
        }
      ]
    }
  ]
}
```

Разрешены только добавления таблиц с типами `string`, `int64`, `float64`,
`bool`, `timestamp`, `json`. Raw SQL, DROP/ALTER и credentials не принимаются.

## 10. Добавьте conformance vectors

Vectors обязательны. Они должны содержать initial state, joins, минимум одну
последовательность команд и private/public projections. Для каждого результата
указывается SHA-256 serialized protobuf bytes.

```json
{
  "room_id": "conformance-room",
  "seed": 42,
  "now_unix_ms": 1700000000000,
  "initial_state_sha256": "...64 hex...",
  "steps": [
    {
      "kind": "join",
      "player_id": "p1",
      "expected_state_sha256": "...",
      "expected_delta_sha256": "..."
    },
    {
      "kind": "command",
      "player_id": "p1",
      "type_url": "type.googleapis.com/acme.mygame.v1.PlayCommand",
      "payload_base64": "CAc=",
      "expected_state_sha256": "...",
      "expected_delta_sha256": "..."
    }
  ],
  "projections": [
    {"player_id":"p1","join_mode":"player","scope":"player","expected_view_sha256":"..."},
    {"player_id":"viewer","join_mode":"spectator","scope":"public","expected_view_sha256":"..."}
  ]
}
```

Ruleshift выполняет vectors дважды и сравнивает все state/delta/view bytes. Любая
недетерминированность блокирует activation.

## 11. Соберите и опубликуйте OCI image

Контейнер должен слушать gRPC на `:50051`, работать без root и принимать token
из `RULESHIFT_MODULE_RPC_TOKEN`.

```powershell
docker build -t registry.example.com/acme/mygame:1.0.0 .
docker push registry.example.com/acme/mygame:1.0.0
docker inspect --format='{{index .RepoDigests 0}}' registry.example.com/acme/mygame:1.0.0
```

Результат должен содержать digest:

```text
registry.example.com/acme/mygame@sha256:0123...
```

Tag-only reference (`:latest`, `:1.0.0`) API отклоняет.

## 12. Зарегистрируйте private registry credential

```powershell
$headers = @{ Authorization = "Bearer $env:RULESHIFT_DEVELOPER_API_KEY" }
$body = @{
  server = "registry.example.com"
  username = "ci-user"
  token = $env:REGISTRY_TOKEN
} | ConvertTo-Json

Invoke-RestMethod -Method Put `
  -Uri "$env:RULESHIFT_URL/v2/developer/registry-credentials/main" `
  -Headers $headers -ContentType application/json -Body $body
```

Credential хранится только как Kubernetes `dockerconfigjson` Secret в tenant
namespace. Он не возвращается API, не пишется в PostgreSQL и не логируется.
Кластер обязан использовать encryption-at-rest для Secrets.

## 13. Создайте module key и опубликуйте version

```powershell
Invoke-RestMethod -Method Post `
  -Uri "$env:RULESHIFT_URL/v2/developer/modules" `
  -Headers $headers -ContentType application/json `
  -Body '{"key":"mygame","display_name":"My Game"}'

curl.exe -X POST `
  -H "Authorization: Bearer $env:RULESHIFT_DEVELOPER_API_KEY" `
  -F "manifest=@manifest.json;type=application/json" `
  -F "descriptor_set=@descriptor.pb;type=application/octet-stream" `
  -F "conformance_vectors=@conformance.json;type=application/json" `
  -F "oci_reference=registry.example.com/acme/mygame@sha256:..." `
  -F "registry_credential=main" `
  "$env:RULESHIFT_URL/v2/developer/modules/mygame/versions"
```

Lifecycle: `validating -> active` или `validating -> failed`. Успешная версия
активируется автоматически; предыдущая active становится inactive.

## 14. Создайте комнату

Комнату создаёт trusted backend, не player build:

```powershell
$room = Invoke-RestMethod -Method Post `
  -Uri "$env:RULESHIFT_URL/v2/rooms" `
  -Headers $headers -ContentType application/json `
  -Body '{"module_id":"mygame"}'
```

Без `version` выбирается active version. Можно явно указать healthy active или
inactive version. Комната навсегда закрепляется за developer/module/version/image
digest. Публикация 1.1.0 не переключит уже созданную комнату 1.0.0.

## 15. Подключите player client

Player WebSocket endpoint: `/v2/ws`. Все frames — binary protobuf
`ruleshift.v2.ClientEnvelope`/`ServerEnvelope`.

1. отправьте `AuthRequest` с `protocol_version = 2`;
2. отправьте `JoinRoomRequest` с заранее созданным `room_id`;
3. упакуйте generated module command в `google.protobuf.Any`;
4. отправьте `GameCommand` с `expected_revision`;
5. распаковывайте snapshot/delta по type URL descriptor модуля.

Protocol v1 намеренно не поддерживается.

## 16. Готовые примеры

Репозиторий содержит три внешних примера:

- `examples/modules/xiangqi`;
- `examples/modules/hiddennumber`;
- `examples/modules/cardgame`.

У каждого есть module proto, manifest, Dockerfile и проверенные conformance
vectors. Общий Go host находится в `examples/modules/runtime`; production core
его не импортирует.

## 17. Диагностика

- `validation_failed: Describe ...` — manifest и код контейнера расходятся;
- `module is nondeterministic` — используете wall clock, random или map iteration;
- `module_unavailable` — обе replicas не готовы или RPC превысил deadline;
- `command_rejected` — модуль отклонил пользовательскую команду;
- `wrong state type` — возвращён не объявленный `state_type_url`;
- `payload ... maximum` — превышен лимит state/command/delta/view;
- после трёх protocol violations за 60 секунд версия становится `degraded`, и
  новые комнаты используют последнюю healthy inactive version.

Для полного сброса pre-production PostgreSQL данных:

```powershell
docker compose down -v
```
