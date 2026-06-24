# Отчёт о производительности

Этот файл служит шаблоном измерений для следующих этапов разработки.

## Целевой сценарий MVP

- 1 000 локально симулируемых игроков.
- 100 активных комнат.
- Бинарный WebSocket-трафик с protobuf.
- Ограниченные очереди комнат и очереди отправки сессий.
- Отсутствие гонок данных при запуске `go test -race ./...`.

## Команды

```powershell
go test ./...
go test -race ./...
go test -bench ./...
go run ./cmd/botload -players 100 -rooms 10 -duration 30s
```

## Планируемые метрики

Счётчики:

- `active_connections`
- `active_rooms`
- `commands_received_total`
- `commands_rejected_total`
- `deltas_sent_total`
- `snapshots_sent_total`
- `reconnects_total`
- `slow_consumers_total`

Гистограммы:

- `command_processing_duration`
- `end_to_end_command_to_delta_duration`
- `protobuf_encode_duration`
- `protobuf_decode_duration`

## Текущие результаты

Результатов сеточного нагрузочного тестирования пока нет. На первом этапе создан только компилируемый каркас проекта.
