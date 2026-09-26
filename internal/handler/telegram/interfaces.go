package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

type ChatUsecaseI interface {
	Allowed(userId int64) bool
	Ask(ctx context.Context, q *chatModel.Question) (*chatModel.Answer, error)
	Reset(ctx context.Context, chatId, userId int64) error
	Admin(userId int64) bool
	Eval(ctx context.Context, userId int64, only []string) (string, error)
}

// NotifyUsecaseI — уведомления агента в чатах: лента, подтверждение, приглушения.
type NotifyUsecaseI interface {
	Chats() []int64
	NotifyChat(chatId int64) bool
	CanManage(chatId, userId int64) bool
	Pending(ctx context.Context, chatId int64) ([]*agentModel.Notification, error)
	Ack(ctx context.Context, chatId, lastId int64, sent, muted, failed int) error
	Mute(ctx context.Context, chatId, userId int64, userName string, notificationId int64, duration string) (*agentModel.Mute, error)
	Unmute(ctx context.Context, chatId, userId, muteId int64) error
	Mutes(ctx context.Context, chatId, userId int64) ([]*agentModel.Mute, []*agentModel.Notification, error)
}

// SenderI — методы Bot API, которыми отвечает бот (реализует *bot.Bot).
type SenderI interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
	SendChatAction(ctx context.Context, params *bot.SendChatActionParams) (bool, error)
	SendPhoto(ctx context.Context, params *bot.SendPhotoParams) (*models.Message, error)
	AnswerCallbackQuery(ctx context.Context, params *bot.AnswerCallbackQueryParams) (bool, error)
}
