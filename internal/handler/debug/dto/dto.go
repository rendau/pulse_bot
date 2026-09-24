// Package dto — JSON отладочной ручки /debug/ask.
package dto

import (
	"strings"

	"github.com/samber/lo"

	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

// лимиты вывода ответа инструмента в trace
const (
	DefaultOutputLimit = 2000
	MaxOutputLimit     = 100 * 1024 // ответ pulse ≤ 100 KB
)

// AskReq — вопрос в отладочный чат.
type AskReq struct {
	// ChatId — номер беседы: у каждого своя история; с чатами Telegram не пересекается
	ChatId int64  `json:"chat_id"`
	Text   string `json:"text"`
	// Reset — забыть историю беседы перед вопросом; без text — только сброс
	Reset bool `json:"reset"`
	// OutputLimit — сколько байт ответа каждого инструмента показать в trace
	// (0 — DefaultOutputLimit, максимум MaxOutputLimit)
	OutputLimit int `json:"output_limit"`
}

// AskRep — ответ бота и ход разбора.
type AskRep struct {
	Answer     string          `json:"answer"`
	Incomplete string          `json:"incomplete,omitempty"`
	Reset      bool            `json:"reset,omitempty"`
	DurationMs int64           `json:"duration_ms"`
	Steps      int             `json:"steps"`
	ToolCalls  int             `json:"tool_calls"`
	Usage      UsageRep        `json:"usage"`
	Trace      []*ToolTraceRep `json:"trace"`
}

type UsageRep struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// ToolTraceRep — вызов инструмента; Output — что ушло модели (с усечением).
type ToolTraceRep struct {
	Step        int    `json:"step"`
	Tool        string `json:"tool"`
	Arguments   string `json:"arguments"`
	Status      string `json:"status"`
	DurationMs  int64  `json:"duration_ms"`
	OutputBytes int    `json:"output_bytes"`
	Truncated   bool   `json:"truncated,omitempty"`
	Output      string `json:"output"`
}

// ErrorRep — ошибка ручки.
type ErrorRep struct {
	Error string `json:"error"`
}

// EncodeAskRep собирает ответ; outputLimit — из AskReq.
func EncodeAskRep(a *chatModel.Answer, outputLimit int) *AskRep {
	limit := lo.Ternary(outputLimit <= 0, DefaultOutputLimit, min(outputLimit, MaxOutputLimit))

	return &AskRep{
		Answer:     a.Text,
		Incomplete: a.Incomplete,
		Steps:      a.Steps,
		ToolCalls:  a.ToolCalls,
		Usage: UsageRep{
			InputTokens:     a.Usage.InputTokens,
			CachedTokens:    a.Usage.CachedTokens,
			OutputTokens:    a.Usage.OutputTokens,
			ReasoningTokens: a.Usage.ReasoningTokens,
		},
		Trace: lo.Map(a.Trace, func(t agentModel.ToolTrace, _ int) *ToolTraceRep {
			output, truncated := truncate(t.Output, limit)
			return &ToolTraceRep{
				Step:        t.Step,
				Tool:        t.Name,
				Arguments:   t.Arguments,
				Status:      t.Status,
				DurationMs:  t.Duration.Milliseconds(),
				OutputBytes: len(t.Output),
				Truncated:   truncated,
				Output:      output,
			}
		}),
	}
}

// truncate режет s до limit байт, не разрывая UTF-8.
func truncate(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	return strings.ToValidUTF8(s[:limit], ""), true
}
