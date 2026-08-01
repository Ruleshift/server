# Ruleshift Developer API v2

Developer API используется Unity Editor tooling, CI или trusted backend.
Bearer key нельзя включать в player build.

Полный контракт находится в `api/developer.openapi.yaml`.

## Локальные prerequisites

Запустите PostgreSQL:

```powershell
docker compose up -d postgres
```

Запустите v2 gateway в Kubernetes либо в окружении, которое может разрешать
имена и обращаться к Kubernetes ClusterIP services. Вне кластера задайте:

```text
RULESHIFT_DATABASE_URL
RULESHIFT_DATABASE_ADMIN_URL
RULESHIFT_DEVELOPER_API_KEY
RULESHIFT_KUBECONFIG
```

## Go SDK

```go
client, err := ruleshift.NewClient(baseURL, developerKey, nil)
module, err := client.CreateRuntimeModule(ctx, "my_game", "My Game")
version, err := client.PublishModuleVersion(ctx, publishRequest)
status, err := client.GetValidationStatus(ctx, module.Key, version.Ref.Version)
room, err := client.CreateRoom(ctx, ruleshift.CreateRoomRequest{
    ModuleID: module.Key,
    PlayerCount: 2,
})
```

`CreateRoom` возвращает шестисимвольный `invite_code` из `0-9A-Z` и
`invite_deadline`, равный ровно 24 часам после `created_at`. `PlayerCount`
должен находиться между `min_players` и `max_players` manifest; ноль или
отсутствующее поле означает `max_players`.

Optional декларативные таблицы модуля доступны через `CreateRow` и `ListRows`.
Ruleshift никогда не возвращает PostgreSQL credentials и не принимает
произвольный SQL.

## Unity и NuGet SDK

Editor-only UPM package находится в `sdk/unity/com.ruleshift.developer`. Тот же
client упаковывается для .NET из `sdk/dotnet/Ruleshift.Developer`.

Доступные операции v2:

- `CreateRuntimeModuleAsync`;
- `PublishModuleVersionAsync`;
- `GetModuleVersionAsync`;
- `GetValidationStatusAsync`;
- `CreateRoomAsync` и `GetRoomAsync`;
- `CreateRowAsync` и `ListRowsAsync`.

## Граница безопасности

- developer keys хранятся в control DB как hashes;
- registry credentials хранятся только в tenant Kubernetes Secrets;
- validation logs ограничены и никогда не содержат registry tokens;
- каждый запрос модуля, версии или комнаты ограничен authenticated developer;
- player authentication остаётся отдельным процессом в protobuf WebSocket protocol v2.
