package chat

import (
	"context"

	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

type AgentI interface {
	Ask(ctx context.Context, req *agentModel.AskReq) (*agentModel.Answer, error)
	Reset(ctx context.Context, conversationId string) error
	Eval(ctx context.Context, only []string) (string, error)
}
