# Создание модулей для Ruleshift

Этот документ описывает два разных вида модулей Ruleshift, способы их создания, ограничения текущей версии и полный пример authoritative-модуля.

## 1. Какой модуль вам нужен

В Ruleshift слово «модуль» используется для двух разных расширений:

| Вид | Для кого | Что добавляет | Нужна сборка gateway |
| --- | --- | --- | --- |
| Data-модуль | Разработчик игры, использующий Ruleshift как сервис | Изолированную БД и пользовательские таблицы | Нет |
| Authoritative game module | Разработчик серверной логики Ruleshift | Правила игры, команды, snapshots, deltas и replay | Да |

Выбирайте data-модуль, если нужны профили, инвентарь, рейтинг, настройки или другая прикладная информация. Он создаётся через SDK или HTTP API.

Выбирайте authoritative-модуль, если Ruleshift должен проверять игровые действия и последовательно изменять состояние комнаты. Такой модуль является частью Go-сервера.

> Важно: создание data-модуля не загружает на сервер новые игровые правила. Оно создаёт только tenant-scoped хранилище. Authoritative-логика всегда исполняется на сервере и не принимается от игрового клиента.

---

# Часть I. Data-модуль через SDK

## 2. Подготовка локального сервиса

Запустите Ruleshift и PostgreSQL:

```powershell
docker compose up --build
```

Локальная конфигурация из `compose.yaml`:

```text
Base URL: http://localhost:8080
Developer API key: ruleshift-dev-key-change-me
```

Для hosted Ruleshift меняются только Base URL и API key. Контракт API остаётся тем же.

Проверка доступности:

```powershell
Invoke-WebRequest http://localhost:8080/readyz
```

Developer API включается только при одновременной настройке:

```text
RULESHIFT_DATABASE_URL
RULESHIFT_DEVELOPER_API_KEY
```

API key определяет developer tenant. Модули и строки другого tenant этим ключом недоступны.

## 3. Ограничения декларативной схемы

Имена модуля, таблиц и столбцов должны соответствовать выражению:

```text
^[a-z][a-z0-9_]{0,47}$
```

Допустимые примеры:

```text
my_game
player_profiles
inventory_v2
```

Недопустимые примеры:

```text
MyGame          # заглавные буквы
my-game         # дефис
2d_game         # начинается с цифры
player profiles # пробел
```

Поддерживаемые типы:

| SDK type | PostgreSQL type | Пример |
| --- | --- | --- |
| `string` | `TEXT` | player id, nickname |
| `int64` | `BIGINT` | rating, currency |
| `float64` | `DOUBLE PRECISION` | координата, коэффициент |
| `bool` | `BOOLEAN` | feature flag |
| `timestamp` | `TIMESTAMPTZ` | время последнего входа |
| `json` | `JSONB` | гибкие настройки |

Дополнительные ограничения:

- не более 32 пользовательских таблиц в одном запросе создания модуля;
- не более 128 столбцов в таблице;
- primary key автоматически становится `NOT NULL`;
- остальные столбцы также `NOT NULL`, если явно не установлен `Nullable`;
- разрешён составной primary key;
- имена `rooms`, `room_players`, `room_events` и `ruleshift_schema_migrations` зарезервированы;
- произвольный SQL через публичный API не принимается.

## 4. Создание data-модуля из Go

Публичный package:

```text
github.com/Ruleshift/server/pkg/ruleshift
```

Установка в отдельный Go-проект:

```powershell
go get github.com/Ruleshift/server/pkg/ruleshift
```

Пример модуля с профилями и инвентарём:

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/Ruleshift/server/pkg/ruleshift"
)

func main() {
    ctx := context.Background()

    client, err := ruleshift.NewClient(
        "http://localhost:8080",
        os.Getenv("RULESHIFT_DEVELOPER_API_KEY"),
        nil,
    )
    if err != nil {
        panic(err)
    }

    module, err := client.CreateModule(ctx, ruleshift.CreateModuleRequest{
        Key:         "my_game",
        DisplayName: "My Game",
        Schema: ruleshift.ModuleSchema{
            Tables: []ruleshift.TableDefinition{
                {
                    Name: "profiles",
                    Columns: []ruleshift.ColumnDefinition{
                        {
                            Name:       "player_id",
                            Type:       ruleshift.ColumnTypeString,
                            PrimaryKey: true,
                        },
                        {
                            Name: "rating",
                            Type: ruleshift.ColumnTypeInt64,
                        },
                        {
                            Name:     "settings",
                            Type:     ruleshift.ColumnTypeJSON,
                            Nullable: true,
                        },
                    },
                },
                {
                    Name: "inventory",
                    Columns: []ruleshift.ColumnDefinition{
                        {
                            Name:       "player_id",
                            Type:       ruleshift.ColumnTypeString,
                            PrimaryKey: true,
                        },
                        {
                            Name:       "item_id",
                            Type:       ruleshift.ColumnTypeString,
                            PrimaryKey: true,
                        },
                        {
                            Name: "quantity",
                            Type: ruleshift.ColumnTypeInt64,
                        },
                    },
                },
            },
        },
    })
    if err != nil {
        panic(err)
    }

    fmt.Printf("created module: %s\n", module.Key)
}
```

Перед запуском:

```powershell
$env:RULESHIFT_DEVELOPER_API_KEY="ruleshift-dev-key-change-me"
go run .
```

## 5. Запись и чтение данных из Go

Создание строки:

```go
row, err := client.CreateRow(ctx, "my_game", "profiles", map[string]any{
    "player_id": "player-1",
    "rating":    int64(1200),
    "settings": map[string]any{
        "language": "ru",
        "music":    true,
    },
})
if err != nil {
    panic(err)
}

fmt.Println(row.Values["player_id"])
```

Получение страницы строк:

```go
page, err := client.ListRows(ctx, "my_game", "profiles", 100, 0)
if err != nil {
    panic(err)
}

for _, row := range page.Rows {
    fmt.Printf("player=%v rating=%v\n", row["player_id"], row["rating"])
}
```

Просмотр фактической схемы:

```go
schema, err := client.GetSchema(ctx, "my_game")
if err != nil {
    panic(err)
}

for _, table := range schema.Tables {
    fmt.Println(table.Name)
    for _, column := range table.Columns {
        fmt.Printf("  %s %s nullable=%t pk=%t\n",
            column.Name,
            column.SQLType,
            column.Nullable,
            column.PrimaryKey,
        )
    }
}
```

Пагинация ограничена 200 строками на запрос. `offset` должен находиться в диапазоне от 0 до 1 000 000.

## 6. Создание data-модуля из Unity/C#

UPM package находится здесь:

```text
sdk/unity/com.ruleshift.developer
```

Добавьте локальный package в `Packages/manifest.json` Unity-проекта:

```json
{
  "dependencies": {
    "com.ruleshift.developer": "file:../server/sdk/unity/com.ruleshift.developer"
  }
}
```

Пример Editor-кода:

```csharp
using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Ruleshift.Developer;

public static class CreateRuleshiftModule
{
    public static async Task Run()
    {
        using var client = new RuleshiftDeveloperClient(
            "http://localhost:8080",
            Environment.GetEnvironmentVariable("RULESHIFT_DEVELOPER_API_KEY"));

        var module = await client.CreateModuleAsync(new CreateModuleRequest
        {
            Key = "my_game",
            DisplayName = "My Game",
            Schema = new ModuleSchema
            {
                Tables =
                {
                    new TableDefinition
                    {
                        Name = "profiles",
                        Columns =
                        {
                            new ColumnDefinition
                            {
                                Name = "player_id",
                                Type = RuleshiftColumnType.String,
                                PrimaryKey = true
                            },
                            new ColumnDefinition
                            {
                                Name = "rating",
                                Type = RuleshiftColumnType.Int64
                            }
                        }
                    }
                }
            }
        });

        await client.CreateRowAsync(
            module.Key,
            "profiles",
            new Dictionary<string, object>
            {
                ["player_id"] = "player-1",
                ["rating"] = 1200L
            });

        var rows = await client.ListRowsAsync(module.Key, "profiles");
        foreach (var row in rows.Rows)
            Console.WriteLine(row["player_id"]);
    }
}
```

Unity package является Editor-only. Developer API key не включается в player build.

Для обычных .NET-инструментов тот же клиент можно собрать как NuGet package:

```powershell
dotnet pack sdk/dotnet/Ruleshift.Developer/Ruleshift.Developer.csproj -c Release
```

## 7. Создание модуля напрямую через HTTP

```powershell
$headers = @{
    Authorization = "Bearer ruleshift-dev-key-change-me"
}

$body = @{
    key = "my_game"
    display_name = "My Game"
    schema = @{
        tables = @(
            @{
                name = "profiles"
                columns = @(
                    @{ name = "player_id"; type = "string"; primary_key = $true },
                    @{ name = "rating"; type = "int64" }
                )
            }
        )
    }
} | ConvertTo-Json -Depth 10

Invoke-RestMethod `
    -Method Post `
    -Uri http://localhost:8080/v1/developer/modules `
    -Headers $headers `
    -ContentType "application/json" `
    -Body $body
```

Полный контракт находится в `api/developer.openapi.yaml`.

## 8. Идемпотентность и изменение схемы

Повторный `CreateModule` с тем же key и полностью той же схемой безопасен: миграция имеет тот же checksum, а запись модуля обновляется через upsert.

Нельзя изменить уже применённую миграцию. Запрос с тем же key и изменённой первой схемой завершится конфликтом. Это защищает работающую БД от незаметного переписывания истории.

В текущей версии публичный API создаёт только первую декларативную миграцию. Для эксперимента с несовместимой схемой используйте новый module key, например:

```text
my_game_v2
```

Для production-эволюции схемы потребуется отдельный endpoint версионированных миграций; его пока нет.

## 9. Доступные операции и текущие ограничения

Data SDK сейчас предоставляет:

- создание модуля;
- список модулей tenant;
- чтение схемы;
- создание строки;
- постраничное чтение строк.

В текущем MVP отсутствуют:

- update/upsert строки;
- delete строки;
- фильтры, сортировка и индексы в декларативном API;
- foreign keys между пользовательскими таблицами;
- публичное управление и ротация API keys.

Не обходите эти ограничения выдачей пользователю PostgreSQL credentials. Расширяйте tenant-scoped API.

---

# Часть II. Authoritative game module на Go

## 10. Контракт authoritative-модуля

Authoritative-модуль реализует интерфейс `game.Module`:

```go
type Module interface {
    Type() Type
    NewState(now time.Time) (any, error)
    PlayerJoined(state any, playerID string) (next any, changed bool, err error)
    Snapshot(state any) (Snapshot, error)
    ProjectSnapshot(state any, viewer Viewer) (ViewSnapshot, error)
    ProjectDelta(before any, after any, delta Delta, viewer Viewer) (ViewDelta, error)
    Apply(state any, command Command) (any, Delta, error)
}
```

Жизненный цикл:

1. `NewState` создаёт начальное состояние комнаты.
2. `PlayerJoined` добавляет серверно подтверждённого игрока.
3. `Apply` проверяет command intent и возвращает новое состояние и delta.
4. Room runtime записывает событие в durable store.
5. Только после успешной записи runtime публикует новое состояние.
6. `Snapshot` формирует полное состояние для reconnect/recovery.
7. После рестарта события повторно проходят через `PlayerJoined` и `Apply`.

Обязательные инварианты:

- не доверять player id или state из клиентского payload;
- `PlayerJoined` и `Apply` не должны менять входной state на месте;
- rejected command не меняет state и revision;
- accepted command увеличивает room revision ровно один раз;
- state hash должен быть детерминированным;
- reducer не должен обращаться к сети;
- reducer не должен создавать goroutine;
- все payload должны иметь ограниченный размер;
- одинаковый начальный state и одинаковая последовательность команд должны давать одинаковый результат.

## 11. Структура нового Go-модуля

Пример для счётчика:

```text
internal/game/counter/
    module.go
    module_test.go
```

Пакет находится внутри `internal`, поэтому authoritative-модуль создаётся в этом репозитории и входит в сборку gateway. Внешний Go-проект не может напрямую реализовать этот контракт без переноса публичного server module SDK в отдельный repository/package.

## 12. Добавление enum значений

В `internal/game/game.go` добавьте уникальный game type и command type:

```go
const (
    TypeUnspecified Type = iota
    TypeXiangqi
    TypeCounter
)

const (
    CommandUnspecified CommandType = iota
    CommandDoMove
    CommandResign
    CommandOfferDraw
    CommandAdd
)
```

Не переиспользуйте существующее числовое значение: оно сохраняется в events и protobuf.

## 13. Полный минимальный reducer

Файл `internal/game/counter/module.go`:

```go
package counter

import (
    "context"
    "encoding/binary"
    "encoding/json"
    "fmt"
    "hash/fnv"
    "math"
    "time"

    "github.com/Ruleshift/server/internal/game"
)

type Module struct{}

type State struct {
    Value int64
}

type Add struct {
    Amount int64 `json:"amount"`
}

type Snapshot struct {
    game.Snapshot
    Value int64
}

type Delta struct {
    game.Delta
    Amount int64
    Value  int64
}

func NewModule() Module {
    return Module{}
}

func (Module) Type() game.Type {
    return game.TypeCounter
}

func (Module) NewState(_ time.Time) (any, error) {
    return &State{}, nil
}

func (Module) PlayerJoined(raw any, playerID string) (any, bool, error) {
    state, err := stateFrom(raw)
    if err != nil {
        return raw, false, err
    }
    if playerID == "" {
        return raw, false, fmt.Errorf("player id must not be empty")
    }

    // Даже если join не меняет предметное состояние, возвращаем отдельную копию.
    next := *state
    return &next, false, nil
}

func (Module) Snapshot(raw any) (game.Snapshot, error) {
    state, err := stateFrom(raw)
    if err != nil {
        return game.Snapshot{}, err
    }

    base := game.Snapshot{
        Type:      game.TypeCounter,
        Status:    game.StatusActive,
        StateHash: stateHash(state.Value),
    }
    payload := Snapshot{
        Snapshot: base,
        Value:    state.Value,
    }
    base.Payload = payload
    return base, nil
}

func (m Module) ProjectSnapshot(raw any, _ game.Viewer) (game.ViewSnapshot, error) {
    snapshot, err := m.Snapshot(raw)
    if err != nil {
        return game.ViewSnapshot{}, err
    }
    return game.ViewSnapshot{
        Type: snapshot.Type, Status: snapshot.Status,
        ViewHash: snapshot.StateHash, Payload: snapshot.Payload,
    }, nil
}

func (Module) ProjectDelta(_ any, _ any, delta game.Delta, _ game.Viewer) (game.ViewDelta, error) {
    return game.ViewDelta{
        Type: delta.Type, CommandType: delta.CommandType,
        Status: delta.Status, ViewHash: delta.StateHash, Payload: delta.Payload,
    }, nil
}

func (Module) Apply(raw any, command game.Command) (any, game.Delta, error) {
    state, err := stateFrom(raw)
    if err != nil {
        return raw, game.Delta{}, err
    }
    if command.PlayerID == "" {
        return raw, game.Delta{}, fmt.Errorf("player id must not be empty")
    }
    if command.Type != game.CommandAdd {
        return raw, game.Delta{}, game.ErrInvalidCommand
    }

    add, err := addFrom(command.Payload)
    if err != nil {
        return raw, game.Delta{}, err
    }
    if add.Amount > 0 && state.Value > math.MaxInt64-add.Amount {
        return raw, game.Delta{}, fmt.Errorf("counter overflow")
    }
    if add.Amount < 0 && state.Value < math.MinInt64-add.Amount {
        return raw, game.Delta{}, fmt.Errorf("counter underflow")
    }

    next := *state
    next.Value += add.Amount

    base := game.Delta{
        Type:           game.TypeCounter,
        CommandType:    game.CommandAdd,
        Status:         game.StatusActive,
        StateHash:      stateHash(next.Value),
        CommandPayload: add,
    }
    payload := Delta{
        Delta:  base,
        Amount: add.Amount,
        Value:  next.Value,
    }
    base.Payload = payload
    return &next, base, nil
}

func stateFrom(raw any) (*State, error) {
    state, ok := raw.(*State)
    if !ok || state == nil {
        return nil, fmt.Errorf("%w: %T", game.ErrUnsupportedState, raw)
    }
    return state, nil
}

func addFrom(raw any) (Add, error) {
    switch payload := raw.(type) {
    case Add:
        return payload, nil
    case *Add:
        if payload != nil {
            return *payload, nil
        }
    }
    return Add{}, fmt.Errorf("%w: expected counter.Add, got %T", game.ErrInvalidCommand, raw)
}

func stateHash(value int64) uint64 {
    var bytes [8]byte
    binary.LittleEndian.PutUint64(bytes[:], uint64(value))
    hash := fnv.New64a()
    _, _ = hash.Write(bytes[:])
    return hash.Sum64()
}

func (Module) MarshalCommandPayload(
    _ context.Context,
    commandType game.CommandType,
    payload any,
) ([]byte, error) {
    if commandType != game.CommandAdd {
        return nil, nil
    }
    add, err := addFrom(payload)
    if err != nil {
        return nil, err
    }
    return json.Marshal(add)
}

func (Module) UnmarshalCommandPayload(
    _ context.Context,
    commandType game.CommandType,
    payload []byte,
) (any, error) {
    if commandType != game.CommandAdd || len(payload) == 0 {
        return nil, nil
    }
    var add Add
    if err := json.Unmarshal(payload, &add); err != nil {
        return nil, fmt.Errorf("decode counter add: %w", err)
    }
    return add, nil
}

var _ game.Module = Module{}
var _ game.CommandPayloadCodec = Module{}
```

Обратите внимание на копирование `next := *state`. Если изменить исходный указатель до durable append, ошибка БД оставит комнату в частично применённом состоянии.

## 14. Собственная схема БД authoritative-модуля

Реализуйте `DatabaseDefinition`, чтобы при старте Ruleshift создал отдельную БД модуля и применил его миграции:

```go
func (Module) DatabaseDefinition() game.DatabaseDefinition {
    return game.DatabaseDefinition{
        Name: "counter",
        Migrations: []game.DatabaseMigration{
            {
                Version: 1,
                Name:    "create_counter_room_metadata",
                SQL: `CREATE TABLE counter_room_metadata (
    room_id TEXT PRIMARY KEY REFERENCES rooms(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);`,
            },
        },
    }
}

var _ game.DatabaseModule = Module{}
```

Правила миграций:

- `Name` соответствует `^[a-z][a-z0-9_]{0,47}$`;
- version положительный и уникальный внутри модуля;
- миграции только forward-only;
- применённый SQL и name нельзя редактировать;
- изменение применённой миграции обнаруживается по SHA-256 checksum;
- следующая миграция получает новую version: 2, 3 и так далее;
- идентификаторы и SQL встроенного модуля считаются доверенным серверным кодом.

Пример второй миграции:

```go
{
    Version: 2,
    Name:    "add_counter_room_description",
    SQL: `ALTER TABLE counter_room_metadata
ADD COLUMN description TEXT;`,
}
```

> Таблицы модуля не обновляются reducer автоматически. Authoritative room state сохраняется в `room_events`. Для custom-таблицы нужен явный repository/projector, вызываемый серверной инфраструктурой, а не игровым клиентом.

## 15. Зачем нужен CommandPayloadCodec

`room_events.command_payload` хранится как JSON. При replay reducer должен получить тот же конкретный Go type, который он получал при live-команде.

Без codec стандартный JSON decode вернёт `map[string]any`, а `Apply` ожидает `counter.Add`. Replay завершится ошибкой.

Поэтому модуль с payload реализует:

```go
type CommandPayloadCodec interface {
    MarshalCommandPayload(ctx context.Context, commandType CommandType, payload any) ([]byte, error)
    UnmarshalCommandPayload(ctx context.Context, commandType CommandType, payload []byte) (any, error)
}
```

Команды без payload могут возвращать `nil, nil`.

## 16. Расширение protobuf-протокола

Добавьте значения и сообщения в `internal/protocol/proto/ruleshift.proto`:

```proto
enum GameType {
  GAME_TYPE_UNSPECIFIED = 0;
  GAME_TYPE_XIANGQI = 1;
  GAME_TYPE_COUNTER = 2;
}

enum GameCommandType {
  GAME_COMMAND_TYPE_UNSPECIFIED = 0;
  GAME_COMMAND_TYPE_DO_MOVE = 1;
  GAME_COMMAND_TYPE_RESIGN = 2;
  GAME_COMMAND_TYPE_OFFER_DRAW = 3;
  GAME_COMMAND_TYPE_ADD = 4;
}

message CounterAdd {
  int64 amount = 1;
}

message CounterSnapshot {
  int64 value = 1;
  uint64 state_hash = 2;
}

message CounterDelta {
  int64 amount = 1;
  int64 value = 2;
  uint64 state_hash = 3;
}
```

Расширьте `GameCommand`:

```proto
message GameCommand {
  string room_id = 1;
  uint64 expected_revision = 2;

  oneof command {
    DoMove do_move = 10;
    Resign resign = 11;
    OfferDraw offer_draw = 12;
    CounterAdd counter_add = 13;
  }
}
```

Расширьте `StateSnapshot.state` и `StateDelta.delta` новыми oneof-полями. Не меняйте номера существующих полей.

Перегенерируйте bindings:

```powershell
.\scripts\proto.ps1
```

После изменения `.proto` commit должен включать:

- исходный `ruleshift.proto`;
- обновлённый Go binding;
- обновлённый C# binding, если он используется Unity SDK.

## 17. Адаптация gateway

В `internal/gateway/gateway.go` функция `toRoomGameCommand` должна преобразовать protobuf payload в module payload:

```go
case *ruleshiftv1.GameCommand_CounterAdd:
    if typed.CounterAdd == nil {
        return room.GameCommand{}, fmt.Errorf("counter_add must not be nil")
    }
    command.Type = game.CommandAdd
    command.Payload = counter.Add{
        Amount: typed.CounterAdd.GetAmount(),
    }
```

В `internal/room/session.go` добавьте преобразования snapshot и delta:

```go
switch snapshot.Game.Type {
case game.TypeXiangqi:
    // существующее преобразование
case game.TypeCounter:
    state.State = &ruleshiftv1.StateSnapshot_Counter{
        Counter: toProtoCounterSnapshot(snapshot.Game),
    }
}
```

Аналогично:

- добавьте `game.TypeCounter` в `toProtoGameType`;
- добавьте `game.CommandAdd` в `toProtoCommandType`;
- реализуйте `toProtoCounterSnapshot`;
- реализуйте `toProtoCounterDelta`;
- добавьте тесты protobuf-конвертации.

## 18. Подключение модуля к gateway

Текущая версия создаёт один `room.Registry` с одним `GameModule` на процесс. Для запуска Counter замените wiring в `cmd/gateway/main.go`:

```go
gameModule := counter.NewModule()
```

Остальная цепочка остаётся прежней:

```go
moduleStore, err := platform.ProvisionModule(ctx, gameModule)

registry := room.NewRegistry(room.RuntimeConfig{
    InputQueueSize: cfg.RoomInputQueueSize,
    EventStore:     moduleStore,
    GameModule:     gameModule,
})
```

> Текущее ограничение: один gateway process обслуживает один authoritative game module. Для одновременной работы Xiangqi и Counter потребуется registry/router по module key или отдельный gateway deployment на модуль. Простого добавления второго `NewModule()` недостаточно.

## ViewScope и неполная информация

`RoomRuntime` хранит полное authoritative-состояние, а клиенту отправляет только результат `ProjectSnapshot` или `ProjectDelta`. Gateway передаёт модулю уже вычисленный `game.Viewer`: модуль не должен читать scope, роль или player id из command payload.

Семантика scope фиксирована:

- `ViewScopePlayer` видит публичные данные и приватные поля только своего `PlayerID`;
- `ViewScopePublic` не видит приватные поля ни одного игрока;
- `ViewScopeFull` видит полное состояние только в сочетании с `JoinModeSpectator`; permission для него подтверждает auth provider;
- `ViewScopeUnspecified` и неизвестные значения работают fail-closed и не раскрывают приватные поля.

Используйте общие helpers вместо самостоятельной проверки enum:

```go
if viewer.CanSeePrivateOf(ownerPlayerID) {
    projected.Secret = &secret // presence означает, что поле разрешено видеть
}

if viewer.CanSeeFullState() {
    // trusted spectator only
}
```

Минимальный шаблон проекции:

```go
func (Module) ProjectSnapshot(raw any, viewer game.Viewer) (game.ViewSnapshot, error) {
    state, err := stateFrom(raw)
    if err != nil {
        return game.ViewSnapshot{}, err
    }
    payload := buildVisibleSnapshot(state, viewer)
    return game.ViewSnapshot{
        Type:     game.TypeExample,
        Status:   state.Status,
        ViewHash: hashVisibleSnapshot(payload),
        Payload:  payload,
    }, nil
}

func (Module) ProjectDelta(before, after any, canonical game.Delta, viewer game.Viewer) (game.ViewDelta, error) {
    beforeView := buildVisibleSnapshot(mustState(before), viewer)
    afterView := buildVisibleSnapshot(mustState(after), viewer)
    result := game.ViewDelta{
        Type:        canonical.Type,
        CommandType: canonical.CommandType,
        Status:      canonical.Status,
        ViewHash:    hashVisibleSnapshot(afterView),
    }
    if hashVisibleSnapshot(beforeView) == result.ViewHash {
        result.NoVisibleChange = true
        return result, nil
    }
    result.Payload = buildVisibleDelta(canonical, viewer)
    return result, nil
}
```

`view_hash` вычисляется после фильтрации и не зависит от скрытых данных. Canonical `Snapshot`, `Delta` и `StateHash` предназначены только для event log и replay; не передавайте их protobuf serializer и не используйте canonical hash на клиенте. Для optional scalar в protobuf проверяйте presence, а не специальное значение вроде нуля.

Обязательная тестовая матрица модуля: оба seated player, public spectator, trusted spectator, unspecified scope, сочетание `FULL + PLAYER` и попытка передать scope через клиентскую команду. Тест должен проверять отсутствие приватного protobuf-поля, разные `view_hash` там, где проекции различаются, и `no_visible_change` для скрытого изменения.

Рабочий пример находится в `internal/game/hiddennumber`.

## 19. Минимальные тесты authoritative-модуля

Создайте `internal/game/counter/module_test.go`:

```go
package counter

import (
    "context"
    "testing"
    "time"

    "github.com/Ruleshift/server/internal/game"
)

func TestModuleAppliesAddWithoutMutatingInput(t *testing.T) {
    module := NewModule()
    original, err := module.NewState(time.Unix(100, 0).UTC())
    if err != nil {
        t.Fatal(err)
    }

    next, delta, err := module.Apply(original, game.Command{
        PlayerID: "player-1",
        Type:     game.CommandAdd,
        Payload:  Add{Amount: 5},
        At:       time.Unix(101, 0).UTC(),
    })
    if err != nil {
        t.Fatal(err)
    }

    originalSnapshot, _ := module.Snapshot(original)
    nextSnapshot, _ := module.Snapshot(next)
    originalPayload := originalSnapshot.Payload.(Snapshot)
    nextPayload := nextSnapshot.Payload.(Snapshot)

    if originalPayload.Value != 0 {
        t.Fatalf("original value = %d, want 0", originalPayload.Value)
    }
    if nextPayload.Value != 5 {
        t.Fatalf("next value = %d, want 5", nextPayload.Value)
    }
    if delta.CommandType != game.CommandAdd {
        t.Fatalf("command type = %d, want add", delta.CommandType)
    }
}

func TestCommandPayloadRoundTrip(t *testing.T) {
    module := NewModule()
    encoded, err := module.MarshalCommandPayload(
        context.Background(),
        game.CommandAdd,
        Add{Amount: 7},
    )
    if err != nil {
        t.Fatal(err)
    }

    decoded, err := module.UnmarshalCommandPayload(
        context.Background(),
        game.CommandAdd,
        encoded,
    )
    if err != nil {
        t.Fatal(err)
    }

    add, ok := decoded.(Add)
    if !ok || add.Amount != 7 {
        t.Fatalf("decoded = %#v, want Add{Amount: 7}", decoded)
    }
}
```

Также нужны тесты:

- invalid payload не меняет state;
- неизвестная команда возвращает `game.ErrInvalidCommand`;
- overflow/underflow отклоняются;
- state hash одинаков для одинакового state;
- replay events восстанавливает revision и state hash;
- snapshot/delta корректно преобразуются в protobuf;
- WebSocket integration test отправляет новую protobuf-команду;
- миграции имеют уникальные версии.

Запуск:

```powershell
go test ./internal/game/counter
go test ./internal/room
go test ./internal/gateway
go test ./...
go vet ./...
```

## 20. Benchmarks для hot path

Для команды модуля добавьте benchmark reducer:

```go
func BenchmarkApplyAdd(b *testing.B) {
    module := NewModule()
    state, _ := module.NewState(time.Unix(100, 0).UTC())
    command := game.Command{
        PlayerID: "player-1",
        Type:     game.CommandAdd,
        Payload:  Add{Amount: 1},
    }

    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        var err error
        state, _, err = module.Apply(state, command)
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

Проверяйте:

- allocations на command apply;
- размер snapshot и delta;
- отсутствие reflection в reducer;
- отсутствие неограниченных slice/map;
- стабильность времени выполнения при росте state.

## 21. Чек-лист готовности authoritative-модуля

- [ ] Добавлен уникальный `game.Type`.
- [ ] Добавлены необходимые `game.CommandType`.
- [ ] `NewState`, `PlayerJoined`, `Snapshot`, `Apply` реализованы.
- [ ] `PlayerJoined` и `Apply` не мутируют входной state.
- [ ] Все команды проверяют server-side identity и правила игры.
- [ ] State hash детерминирован.
- [ ] Payload codec обеспечивает replay конкретного Go type.
- [ ] Database migrations forward-only и имеют уникальные versions.
- [ ] Protobuf расширен без изменения старых field numbers.
- [ ] Gateway преобразует новый command payload.
- [ ] Snapshot и delta сериализуются в protobuf.
- [ ] Module подключён к `cmd/gateway`.
- [ ] Есть reducer, replay, protocol и WebSocket тесты.
- [ ] Добавлен benchmark hot path.
- [ ] Выполнены `go test ./...` и `go vet ./...`.
- [ ] Обновлены README и protocol/architecture docs.

## 22. Частые ошибки

### Изменение state до durable append

Плохо:

```go
state.Value += amount
return state, delta, nil
```

Хорошо:

```go
next := *state
next.Value += amount
return &next, delta, nil
```

### Хранение только snapshot без events

Snapshot полезен для reconnect, но authoritative recovery строится на ordered event stream. Не принимайте snapshot от клиента как источник истины.

### Отсутствие payload codec

Live-команда может работать, но replay после рестарта получит `map[string]any` и упадёт. Добавляйте round-trip test codec.

### Изменение применённой миграции

Не исправляйте migration version 1 задним числом. Создайте version 2.

### Секрет developer API key в player build

Developer SDK предназначен для Editor, CI и trusted backend. Player использует auth ticket и protobuf WebSocket API.

### Попытка зарегистрировать несколько authoritative-модулей в одном Registry

`room.Registry` сейчас хранит один `RuntimeConfig.GameModule`. Для нескольких игр нужен явный router/registry архитектурный слой.

## 23. Полезные файлы проекта

| Назначение | Путь |
| --- | --- |
| Контракт game module | `internal/game/game.go` |
| Пример Xiangqi | `internal/game/xiangqi/module.go` |
| Тесты Xiangqi | `internal/game/xiangqi/module_test.go` |
| Room reducer integration | `internal/room/command.go` |
| Event replay | `internal/room/replay.go` |
| Protobuf schema | `internal/protocol/proto/ruleshift.proto` |
| Protobuf adapters | `internal/room/session.go` |
| Gateway command adapter | `internal/gateway/gateway.go` |
| Gateway wiring | `cmd/gateway/main.go` |
| Developer Go SDK | `pkg/ruleshift` |
| Unity package | `sdk/unity/com.ruleshift.developer` |
| OpenAPI | `api/developer.openapi.yaml` |
| Database architecture | `docs/database.md` |
| Service API guide | `docs/developer-api.md` |
