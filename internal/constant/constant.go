package constant

import "strconv"

const (
	ServiceName = "pulse_bot"
)

// Version подставляется при сборке: -ldflags "-X .../internal/constant.Version=<ver>".
var Version = "dev"

// LLM-провайдеры (LLM_PROVIDER)
const (
	LlmProviderOpenai = "openai"
)

// исход вопроса (метрики)
const (
	OutcomeAnswered   = "answered"
	OutcomeIncomplete = "incomplete"
	OutcomeDenied     = "denied"
	OutcomeBusy       = "busy"
	OutcomeError      = "error"
)

// ConversationId — беседа чата Telegram в агенте: история, заметки, лента уведомлений и приглушения.
func ConversationId(chatId int64) string {
	return "tg:" + strconv.FormatInt(chatId, 10)
}
