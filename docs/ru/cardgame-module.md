# Card Game как модуль protocol v2

Card Game больше не запускается внутри Ruleshift core. Внешний OCI-пример
находится в `examples/modules/cardgame` и реализует Module Runtime ABI v2.

Образ поддерживает:

- неизменяемую конфигурацию 2–6 мест, переданную Ruleshift Core;
- детерминированное создание колоды из seed комнаты Ruleshift;
- приватные руки для player scope;
- публичное количество карт для spectators;
- полные руки для trusted full scope;
- команды `start`, `play_card`, `attach_modifier` и `end_turn`;
- аутентифицированных actors по местам и детерминированные conformance vectors.

Сборка из корня репозитория:

```powershell
docker build -f examples/modules/cardgame/Dockerfile `
  -t registry.example.com/cardgame:2.0.0 .
docker push registry.example.com/cardgame:2.0.0
```

Опубликуйте полученный `registry.example.com/cardgame@sha256:...` вместе с:

- `examples/modules/cardgame/manifest.json`;
- сгенерированным `descriptor.pb`;
- `examples/modules/cardgame/conformance.json`.

В этом компактном примере module-specific protobuf `Command` содержит canonical
JSON в поле 1. Команды выглядят так:

```json
{"kind":"start"}
{"kind":"play_card","card_id":"card-1-0"}
{"kind":"attach_modifier","card_id":"modifier-id","target_card_id":"target-id"}
{"kind":"end_turn"}
```

Production-игра может заменить этот envelope строго типизированными protobuf
полями, не меняя ABI или core Ruleshift.
