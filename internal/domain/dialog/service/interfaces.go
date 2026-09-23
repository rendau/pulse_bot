package service

import (
	"context"

	"github.com/mechta-market/pulse_bot/internal/domain/dialog/model"
)

type RepoI interface {
	List(ctx context.Context, chatId int64) ([]*model.Turn, error)
	Replace(ctx context.Context, chatId int64, turns []*model.Turn) error
}
