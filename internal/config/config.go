package config

import (
	"time"

	"github.com/caarlos0/env/v9"
	_ "github.com/joho/godotenv/autoload"
)

// Conf — параметры окружения: подключения, порты, токены, лимиты.
var Conf = struct {
	Debug    bool   `env:"DEBUG" envDefault:"false"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	SystemHttpPort string `env:"SYSTEM_HTTP_PORT" envDefault:"3003"` // healthcheck, metrics, docs

	// telegram: long polling. Разрешённые чаты через запятую: личные — ID человека, группы — с
	// минусом. В разрешённом чате можно спрашивать, настраивать уведомления (по подписке, без неё —
	// не приходят) и заметки чата; в группе — любой участник
	TelegramBotToken       string  `env:"TELEGRAM_BOT_TOKEN,required"`
	TelegramAllowedChatIds []int64 `env:"TELEGRAM_ALLOWED_CHAT_IDS" envSeparator:","`
	// админы (user ID): команда /eval; их личные чаты разрешены автоматически
	TelegramAdminUsers []int64 `env:"TELEGRAM_ADMIN_USERS" envSeparator:","`

	// как часто забирать у агента ленту уведомлений разрешённых чатов
	NotifyPollInterval time.Duration `env:"NOTIFY_POLL_INTERVAL" envDefault:"20s"`

	// pulse_agent: разбор вопроса, история бесед и графики — там; ключ — бота в API_KEYS агента
	AgentUrl string `env:"AGENT_URL,required"`
	AgentKey string `env:"AGENT_KEY"`
	// AgentTimeout — сколько ждать ответ агента (его разбор — до 5 мин, ответ приходит целиком)
	AgentTimeout time.Duration `env:"AGENT_TIMEOUT" envDefault:"6m"`
	// AgentEvalTimeout — сколько ждать прогон эталонов (/eval)
	AgentEvalTimeout time.Duration `env:"AGENT_EVAL_TIMEOUT" envDefault:"21m"`
}{}

func init() {
	if err := env.Parse(&Conf); err != nil {
		panic(err)
	}
}
