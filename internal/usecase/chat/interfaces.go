package chat

import (
	"context"

	dialogModel "github.com/mechta-market/pulse_bot/internal/domain/dialog/model"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

type DialogServiceI interface {
	History(ctx context.Context, chatId int64) ([]*dialogModel.Turn, error)
	Append(ctx context.Context, chatId int64, question, answer string) error
	Reset(ctx context.Context, chatId int64) error
}

type AgentI interface {
	Run(ctx context.Context, req *agentModel.Req) (*agentModel.Result, error)
}
