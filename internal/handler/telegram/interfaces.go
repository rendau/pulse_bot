package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

type ChatUsecaseI interface {
	Allowed(userId int64) bool
	Ask(ctx context.Context, q *chatModel.Question) (*chatModel.Answer, error)
	Reset(ctx context.Context, chatId, userId int64) error
}

// SenderI — методы Bot API, которыми отвечает бот (реализует *bot.Bot).
type SenderI interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
	SendChatAction(ctx context.Context, params *bot.SendChatActionParams) (bool, error)
	SendPhoto(ctx context.Context, params *bot.SendPhotoParams) (*models.Message, error)
}
