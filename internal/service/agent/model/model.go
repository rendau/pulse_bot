package model

import (
	"time"

	llmModel "github.com/mechta-market/pulse_bot/internal/service/llm/model"
)

// причины, по которым разбор закончен раньше, чем модель решила сама
const (
	IncompleteToolCalls = "tool_calls" // исчерпан лимит вызовов инструментов
	IncompleteTimeout   = "timeout"    // исчерпано время на разбор
	IncompleteOutput    = "output"     // ответ модели оборван провайдером (лимит токенов и т.п.)
)

// статусы вызова инструмента (ToolTrace.Status, метрики)
const (
	ToolStatusOk        = "ok"
	ToolStatusError     = "error"      // вызов не удался (pulse недоступен и т.п.)
	ToolStatusToolError = "tool_error" // инструмент ответил ошибкой (неверный параметр и т.п.)
	ToolStatusSkipped   = "skipped"    // не выполнен: исчерпан лимит вызовов
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

	// Trace — вызовы инструментов по порядку (ход разбора для отладки).
	Trace []ToolTrace

	// Charts — графики к ответу (render_chart), по порядку построения.
	Charts []Chart
}

// Chart — картинка графика к ответу.
type Chart struct {
	Title string
	Png   []byte
}

// ToolTrace — вызов инструмента в ходе разбора.
type ToolTrace struct {
	Step      int // шаг модели, запросивший вызов (с 1)
	Name      string
	Arguments string
	Status    string // ToolStatus*
	Output    string // что ушло модели
	Duration  time.Duration
	Chart     *Chart // построенный график (render_chart)
}
