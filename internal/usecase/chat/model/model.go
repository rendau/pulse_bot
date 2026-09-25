package model

import (
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	llmModel "github.com/mechta-market/pulse_bot/internal/service/llm/model"
)

// Question — вопрос пользователя в чате.
type Question struct {
	ChatId int64
	UserId int64
	Text   string
}

// Answer — ответ бота. Incomplete — почему разбор закончен досрочно
// (agent/model.Incomplete*); пусто — ответ полный.
type Answer struct {
	Text       string
	Incomplete string
	// Charts — картинки графиков к ответу
	Charts []agentModel.Chart

	// ход разбора (отладочная ручка; Telegram их не показывает)
	Steps     int
	ToolCalls int
	Usage     llmModel.Usage
	Trace     []agentModel.ToolTrace
}
