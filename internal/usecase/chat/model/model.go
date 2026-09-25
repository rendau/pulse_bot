package model

import agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"

// Question — вопрос пользователя в чате.
type Question struct {
	ChatId   int64
	UserId   int64
	UserName string
	Text     string
}

// Answer — ответ бота. Incomplete — почему разбор закончен досрочно (timeout, tool_calls,
// output); пусто — ответ полный.
type Answer struct {
	Text       string
	Incomplete string
	Charts     []agentModel.Chart
}
