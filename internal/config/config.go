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

	// telegram: long polling; отвечаем только пользователям из белого списка (user ID через запятую)
	TelegramBotToken     string  `env:"TELEGRAM_BOT_TOKEN,required"`
	TelegramAllowedUsers []int64 `env:"TELEGRAM_ALLOWED_USERS" envSeparator:","`
	// админы: команда /eval (прогон эталонных вопросов агента)
	TelegramAdminUsers []int64 `env:"TELEGRAM_ADMIN_USERS" envSeparator:","`

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
