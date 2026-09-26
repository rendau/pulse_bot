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

	// Notifications — новые уведомления беседы (первый вызов подписывает с текущего места — пусто)
	Notifications(ctx context.Context, conversationId string, limit int) ([]*agentModel.Notification, error)
	// Ack — беседа получила уведомления до lastId включительно
	Ack(ctx context.Context, conversationId string, lastId int64) error
	Mute(ctx context.Context, req *agentModel.MuteReq) (*agentModel.Mute, error)
	Unmute(ctx context.Context, conversationId string, id int64) error
	// Mutes — действующие приглушения беседы и последние скрытые ими уведомления
	Mutes(ctx context.Context, conversationId string, mutedLimit int) ([]*agentModel.Mute, []*agentModel.Notification, error)
	// Subscriptions — подписки беседы (пусто — приходит всё)
	Subscriptions(ctx context.Context, conversationId string) ([]*agentModel.Subscription, error)
	Unsubscribe(ctx context.Context, conversationId string, id int64) error
}
