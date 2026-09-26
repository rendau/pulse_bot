package notify

import (
	"context"

	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

type AgentI interface {
	Notifications(ctx context.Context, conversationId string, limit int) ([]*agentModel.Notification, error)
	Ack(ctx context.Context, conversationId string, lastId int64) error
	Mute(ctx context.Context, req *agentModel.MuteReq) (*agentModel.Mute, error)
	Unmute(ctx context.Context, conversationId string, id int64) error
	Mutes(ctx context.Context, conversationId string, mutedLimit int) ([]*agentModel.Mute, []*agentModel.Notification, error)
}
