# pulse_bot

Telegram-бот для разговора об инфраструктуре поверх MCP-сервера pulse.
Что строим и принятые решения — [docs/bot-spec.md](docs/bot-spec.md).

### Сборка и запуск

```
make build
go run ./cmd/.
```

Переменные окружения — `internal/config/config.go`, пример — `.env.example`.
