// Package mem — история диалогов в памяти процесса (v1: без БД, рестарт пода
// историю сбрасывает).
package mem

import (
	"context"
	"slices"
	"sync"

	"github.com/mechta-market/pulse_bot/internal/domain/dialog/model"
)

type Repo struct {
	mu    sync.Mutex
	chats map[int64][]*model.Turn
}

func New() *Repo {
	return &Repo{chats: map[int64][]*model.Turn{}}
}

func (r *Repo) List(_ context.Context, chatId int64) ([]*model.Turn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.chats[chatId]), nil
}

func (r *Repo) Replace(_ context.Context, chatId int64, turns []*model.Turn) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(turns) == 0 {
		delete(r.chats, chatId)
		return nil
	}

	r.chats[chatId] = slices.Clone(turns)
	return nil
}
