package agent

import (
	"context"

	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

// Agent — клиент API pulse_agent (github.com/mechta-market/pulse_agent, docs/agent-api.md):
// разбор вопроса, история бесед и графики — там; бот только передаёт вопрос и ответ.
type Agent interface {
	Ask(ctx context.Context, req *agentModel.AskReq) (*agentModel.Answer, error)
	Reset(ctx context.Context, conversationId string) error
	// Eval — прогон эталонных вопросов агента (only — id; пусто — все): таблица текстом
	Eval(ctx context.Context, only []string) (string, error)
}
