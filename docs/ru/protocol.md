# Протоколы Ruleshift

В Ruleshift используются два независимых protobuf-контракта.

## Player WebSocket v2

Схема: `internal/protocol/proto/ruleshift.proto`. Endpoint: `/v2/ws`. Каждый бинарный WebSocket-фрейм содержит ровно один сериализованный envelope без дополнительного префикса длины. Поле `protocol_version` должно быть равно `2`; протокол v1 отклоняется.

Последовательность взаимодействия:

1. Первым envelope обязательно отправляется `AuthRequest`.
2. `JoinRoomRequest.invite_code` содержит шестисимвольный код из ответа
   `POST /v2/rooms`; сервер находит по действующему коду внутренний ID комнаты.
3. `JoinRoomOk.room_id` возвращает этот внутренний ID, после чего сервер
   отправляет персонализированный для получателя `StateSnapshot`.
4. Игрок отправляет `GameCommand` с аутентифицированной командой в `Any`.
5. Сервер последовательно обрабатывает команду через очередь комнаты и рассылает персонализированные `StateDelta` в порядке ревизий.

Core-сообщения не содержат игровых enum или oneof. `ModuleRef` определяет закреплённые модуль и версию, а состояние, команда и delta передаются через `google.protobuf.Any`. `view_digest` — это SHA-256 точного protobuf-payload после проекции.

Ошибка или тайм-аут модуля приводит к `module_unavailable` или `command_rejected` и никогда не изменяет ревизию комнаты.

## Module Runtime ABI v2

Схема: `internal/moduleruntime/proto/module_runtime.proto`. Это обычный gRPC-сервис на порту 50051 внутри Kubernetes-кластера. Сервис реализует `Describe`, `CreateState`, `Apply`, `ProjectSnapshot` и `ProjectDelta`. В ABI v2 нет RPC комнаты, лобби, подключения, Join, Leave или reconnect.

Процесс модуля не владеет текущими матчами. Ruleshift хранит opaque canonical state в комнате и передаёт его в каждый transition/projection. `CreateState` получает только неизменяемый `GameSetup.player_count`. Core хранит устойчивое соответствие аутентифицированного `player_id <-> seat_index`, запрещает команды до заполнения всех мест, освобождает место при отключении в статусе `lobby` и сохраняет его после перехода в `active`.

`Apply` получает проверенный `Actor { player_id, seat_index }` отдельно от команды клиента. Проекции получают `Viewer` с optional местом и scope player/public/full. Детерминированный контекст содержит только revision, серверное время и seed комнаты. Модуль возвращает непрозрачные protobuf-данные; состоянием и порядком ревизий управляет Ruleshift.

Ограничения:

- состояние: 1 МиБ;
- команда, delta и view: 256 КиБ;
- deadline transition: по умолчанию 50 мс, не более 250 мс;
- `CreateState`: не более 250 мс;
- один повторный вызов разрешён только для `Unavailable` и только в пределах исходного deadline.

Трафик модуля защищён случайным bearer-токеном для каждого Deployment, который хранится в Kubernetes Secret. Учётные данные игроков и разработчиков никогда не передаются в pod-ы модулей.

## Генерация

```powershell
./scripts/proto.ps1
```

Сгенерированные Go-пакеты называются `ruleshiftv2` и `moduleruntimev2`. Авторы модулей генерируют ABI bindings и bindings собственных игровых proto-файлов в отдельном репозитории модуля.
