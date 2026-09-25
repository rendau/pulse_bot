package model

// причины, по которым агент закончил разбор досрочно (Answer.Incomplete)
const (
	IncompleteToolCalls = "tool_calls"
	IncompleteTimeout   = "timeout"
	IncompleteOutput    = "output"
)

// AskReq — вопрос агенту из чата.
type AskReq struct {
	ConversationId string // беседа: у бота — чат Telegram
	UserId         string
	UserName       string
	Question       string
}

// Answer — ответ агента.
type Answer struct {
	Text string
	// Incomplete — разбор закончен досрочно: timeout | tool_calls | output; пусто — полный
	Incomplete string
	Charts     []Chart
}

// Chart — картинка графика к ответу.
type Chart struct {
	Title string
	Png   []byte
}
