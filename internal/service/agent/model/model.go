package model

import (
	llmModel "github.com/mechta-market/pulse_bot/internal/service/llm/model"
)

// причины, по которым разбор закончен раньше, чем модель решила сама
const (
	IncompleteToolCalls = "tool_calls" // исчерпан лимит вызовов инструментов
	IncompleteTimeout   = "timeout"    // исчерпано время на разбор
	IncompleteOutput    = "output"     // ответ модели оборван провайдером (лимит токенов и т.п.)
)

// Turn — прошлая пара «вопрос — ответ» из истории чата.
type Turn struct {
	Question string
	Answer   string
}

// Req — вопрос с историей чата.
type Req struct {
	History  []Turn
	Question string
}

// Result — итог разбора.
type Result struct {
	Answer string

	// Incomplete — почему разбор закончен досрочно (Incomplete*); пусто — модель
	// ответила сама.
	Incomplete string

	Steps     int // шагов модели
	ToolCalls int // вызовов инструментов pulse
	Usage     llmModel.Usage
}
