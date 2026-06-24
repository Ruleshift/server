# Игровой клиент Unity

Транспорт игрока использует бинарный protobuf-протокол WebSocket v2 по адресу `/v2/ws`. Сгенерируйте C# bindings из `internal/protocol/proto/ruleshift.proto`, а также bindings собственных protobuf-сообщений выбранного модуля в игровом проекте.

## Сценарий взаимодействия

1. Подключитесь к `ws://host:port/v2/ws`.
2. Отправьте `ClientEnvelope` с `protocol_version = 2` и `AuthRequest`.
3. Присоединитесь к комнате, ранее созданной доверенным backend через `POST /v2/rooms`.
4. Прочитайте `ModuleRef` и распакуйте значения `Any` из snapshot/delta при помощи bindings модуля.
5. Упакуйте команды модуля в `Any` и отправьте `GameCommand` с последней полученной ревизией.
6. Применяйте snapshots и deltas только в порядке серверных ревизий.

Developer API key никогда не должен попадать в пользовательскую сборку Unity. Он используется только в Editor-инструментах, CI или доверенном backend.

```csharp
var envelope = new ClientEnvelope {
    ProtocolVersion = 2,
    ClientSequence = ++sequence,
    GameCommand = new GameCommand {
        RoomId = roomId,
        ExpectedRevision = revision,
        Command = Any.Pack(moduleCommand)
    }
};
```

`view_digest` — это SHA-256 точного protobuf-payload после проекции. С его помощью можно обнаруживать рассинхронизацию клиента, не раскрывая каноническое приватное состояние.
