package debug

import (
	"context"

	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

type ChatUsecaseI interface {
	Ask(ctx context.Context, q *chatModel.Question) (*chatModel.Answer, error)
	Reset(ctx context.Context, chatId, userId int64) error
}
