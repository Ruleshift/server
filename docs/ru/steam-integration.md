# Steam-совместимая аутентификация

Ruleshift сохраняет возможность замены поставщика аутентификации через интерфейс:

```go
type Provider interface {
    AuthenticateTicket(ctx context.Context, ticket string) (*Identity, error)
}
```

## Локальная разработка

`MockProvider` принимает билеты следующего вида:

```text
mock:player-1
```

Он возвращает стабильный серверный `PlayerID`.

## Планируемый сценарий Steam

1. Unity получает Steam auth session ticket через Steamworks.NET или другой мост к Steamworks.
2. Unity отправляет билет в `AuthRequest`.
3. Go gateway вызывает `SteamWebAPIProvider`.
4. Поставщик проверяет владение игрой и Steam identity через Steam Web API.
5. Gateway связывает проверенную identity с WebSocket-сессией протокола v2.
6. `roomcore` получает только аутентифицированную сервером identity игрока.

## Примечания по безопасности

- Никогда не помещайте ключи Steam Web API в Unity-клиент.
- Читайте API-ключи только из переменных окружения или менеджера секретов.
- Не позволяйте клиентам самостоятельно выбирать `player_id`.
- Сохраняйте логику комнат независимой от Steam-специфичных полей.
