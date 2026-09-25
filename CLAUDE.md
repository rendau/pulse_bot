# CLAUDE.md

Руководство для Claude Code по работе с этим репозиторием. Go-сервис на Clean Architecture + DDD:
Telegram-бот (long polling, `github.com/go-telegram/bot`) — тонкий клиент агента pulse_agent
(github.com/mechta-market/pulse_agent). Разбор вопроса — LLM, инструменты pulse, промпт, графики,
история бесед, эталонные вопросы — живёт в агенте (контракт — его `docs/agent-api.md`); бот
отвечает за Telegram: белый список, «печатает…», Markdown → HTML, фото графиков.

**Перед любой работой прочитать `docs/bot-spec.md`** — что строим, принятые решения, согласованная
первая версия и что отложено на потом.

Согласованные отклонения от шаблона gotemplate:
- gRPC/grpc-gateway/proto/swagger, Postgres и миграции, трассировка убраны: транспорт — Telegram.

Конвенции этого стека вынесены в глобальные Claude Code скиллы (`golang-service`,
`golang-samber-lo`, `crud`, `mobone`) — они подхватываются автоматически по описанию.

---

## Структура проекта

### Верхний уровень
- `cmd/main.go` — entrypoint, поднимает `internal/app.App`.
- `internal/` — бизнес-логика и инфраструктура (закрытые пакеты).
- `docs/` — вводная и статические доки, выдаются через `/docs/*`.
- `Dockerfile`, `Makefile` — сборка (`make build` подставляет версию через ldflags).
- `.env.example` — пример окружения.

### Внутренние пакеты (`internal/`)
- `internal/app/` — сборка приложения: DI, запуск Telegram-бота и системного HTTP-сервера.
  - `app.go` — граф зависимостей, жизненный цикл.
  - `system_http_server.go` — системный HTTP-сервер (`SYSTEM_HTTP_PORT`, дефолт 3003):
    /healthcheck, /docs/*, /metrics.
- `internal/config/` — конфигурация через env (`config.go`).
- `internal/handler/telegram/` — транспорт: личные сообщения, команды `/start` `/help` `/reset` (и кнопка сброса под полем ввода),
  «печатает…», отправка ответа (Markdown → Telegram HTML, нарезка под 4096, фолбэк на plain text),
  графики — фото после текста, тексты ответов бота — `texts.go`.
- `internal/usecase/chat/` — вопрос: белый список, «один вопрос за раз на чат», агент, метрики
  вопросов. Беседа в агенте — `tg:<chat_id>`; `/reset` — `POST /v1/reset` агента.
- `internal/service/agent/` — клиент API pulse_agent (раскладка — скилл `golang-service`):
  `POST /v1/ask` с `format=telegram`, `charts=png`, `user` — id и имя из Telegram; коды ошибок
  агента → `internal/errs` (`busy` → `Busy`, `timeout` → `DeadlineExceeded`, прочее → `ServiceNA`).
- `internal/infra/httpx/` — единая фабрика http-клиентов (таймауты, лимиты; все клиенты только через неё).
- `internal/infra/metrics/` — реестр Prometheus.
- `internal/util/tgmd/` — Markdown → Telegram HTML и нарезка сообщения (с тестами).
- `internal/errs/` и `internal/constant/` — общие коды ошибок и константы.

---

## Архитектура: слои и зависимости

- **Transport** (`internal/handler/telegram`):
  - Работает только с usecase-интерфейсами и моделями usecase.
  - Не обращается напрямую к репозиториям и сервисам.
- **Usecase** (`internal/usecase/*`):
  - Входной слой от транспортного слоя (запросы от внешних систем).
  - Валидация входных параметров и доступ (белый список).
  - Оркестрация сервисов `internal/service/*`.
  - Вход/выход — модели usecase и сервисов.
- **Service** (`internal/service/*`):
  - Интеграции с внешними системами (агент).
  - Не обращается в usecase слой.
- **Composition** (`internal/app/`):
  - Сборка зависимостей, запуск бота и серверов.

### Правило зависимостей
```
handler        → usecase
usecase        → service
service        → service
```
- Обратные зависимости **запрещены**.

### Ошибки и валидация
- Семантические ошибки — через `internal/errs` (`NotAuthorized`, `Busy`, `InvalidRequest`,
  `ServiceNA`); handler переводит их в тексты ответа.
- Нельзя пробрасывать ошибки наружу без wrapping (оборачивать в `fmt.Errorf("...: %w")`).
- Для работы с ошибками всегда используй `errors.Is` и `errors.AsType`. Избегай прямого
  сравнения ошибок (`==`) и type assertion (`err.(*MyError)`), чтобы корректно обрабатывать
  обёрнутые ошибки.

### Правила изменения кода
- Модели usecase и сервисов не должны протекать в транспорт Telegram дальше handler'а.
- В тестах всегда предпочитай `testify`: `require` для проверок, прерывающих тест,
  и `assert` для остальных утверждений.
- http-клиенты — только через `internal/infra/httpx`. Клиент Telegram — с проверкой TLS
  (`VerifyTLS: true`), http-таймаут больше long polling; клиент агента — внутри кластера,
  `ResponseHeaderTimeout` = `AGENT_TIMEOUT` (ответ приходит целиком после разбора).
- Правила ответа (единицы, формат, графики) — в промпте агента, не здесь; проверка — эталонные
  вопросы агента (`make eval` в pulse_agent).

---

## Композиционный корень (`internal/app/app.go`)

`app.go` — единственная точка композиции приложения: граф зависимостей (`service → usecase →
handler`) и жизненный цикл. Бизнес-логики тут нет.

### Тип `App`
- В поля выносится **только то, чем нужно управлять после `Init`**: бот (останавливать), ожидание
  опроса и обработки сообщений, системный сервер, корневой `ctx` с его `ctxCancel` и `exitCode`.
- Локальные звенья графа — локальные переменные внутри `Init`.

### Импорты Композиционного корня
- Группируются блоками с пустой строкой между группами: стандартная библиотека → внешние
  зависимости → внутренние пакеты проекта.
- Внутренние пакеты-конструкторы импортируются с суффиксом-алиасом `P`, и в алиасе прописывается весь путь в camel-case
  (например, `internal/service/agent/service` -> `serviceAgentServiceP`, `internal/handler/telegram` -> `handlerTelegramP`).

### Методы-фазы жизненного цикла
- `Init` — создание и связывание всех зависимостей.
- `PreStartHook` — действия перед стартом.
- `Start` — запуск бота и серверов.
- `Listen` — блокировка до сигнала ОС (`SIGINT`/`SIGTERM`).
- `Stop` — отмена контекста (останавливает long polling и отменяет ожидание ответов агента —
  пользователю уходит «бот перезапускается») и graceful-остановка серверов.
- `WaitJobs` — ожидание опроса Telegram и обработки принятых сообщений.
- `Exit` — выход с `exitCode`.

### Обработка ошибок при инициализации
- Хелпер `errCheck(err, msg)`: на этапе сборки любая ошибка фатальна (лог + `os.Exit(1)`).
- Недоступный агент — **не** ошибка старта: бот отвечает «не удалось получить ответ».

### Парность Start / WaitJobs / Stop
- Для каждого фонового компонента, запускаемого в `Start()`, есть симметричный вызов в
  `WaitJobs()` (`.Wait()`) и/или в `Stop()`, порядок перечисления одинаков.

### Конфигурация
- Все параметры берутся из единого глобального конфига (`config.Conf.*`) прямо в месте
  использования. Весь env сервиса — в kusec (app `pulse`, `bot`), у деплоймента своих env нет.

---

## Runtime и конфигурация

### Переменные окружения
- Описаны в `internal/config/config.go`, пример — `.env.example`.
- Обязательные: `TELEGRAM_BOT_TOKEN`, `AGENT_URL` (`http://pulse-agent.default`); `AGENT_KEY` — ключ
  бота в `API_KEYS` агента. Пустой `TELEGRAM_ALLOWED_USERS` — бот отказывает всем (предупреждение в логе).

### Метрики
- Prometheus на `/metrics` (системный сервер) при `WITH_METRICS=true`, реестр `metrics.Registry`.
- `question_total{outcome}`, `answer_duration_seconds`. Метрики LLM и инструментов — в агенте.

### Сборка
- `make build` создаёт бинарник `cmd/build/svc`.
- Dockerfile копирует бинарник и `docs/` в `/app`.

### Flow проверки изменений
```
gofmt  →  go vet ./...  →  go test ./...  →  go run ./cmd/.
```
