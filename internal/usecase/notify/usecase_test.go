package notify

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_bot/internal/errs"
	agentModel "github.com/rendau/pulse_bot/internal/service/agent/model"
)

func TestCanManage(t *testing.T) {
	u := New(Config{AllowedChats: []int64{-100, -100, 42}}, nil)

	assert.Equal(t, []int64{-100, 42}, u.Chats())
	assert.True(t, u.CanManage(-100), "разрешённая группа — любой участник")
	assert.True(t, u.CanManage(42))
	assert.False(t, u.CanManage(999))

	_, err := u.Mute(context.Background(), 999, 999, "", 1, "")
	require.ErrorIs(t, err, errs.NotAuthorized)
}

// fakeAgent — блокировка: запоминает запросы, отвечает err или успехом.
type fakeAgent struct {
	AgentI
	reqs []*agentModel.BlockReq
	err  error
}

func (f *fakeAgent) Block(_ context.Context, req *agentModel.BlockReq) (*agentModel.Block, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	return &agentModel.Block{NotificationId: req.NotificationId, Ip: "203.0.113.7/32", Action: req.Action}, nil
}

func TestBlock(t *testing.T) {
	agent := &fakeAgent{}
	u := New(Config{AllowedChats: []int64{-100, 42}, AdminUsers: []int64{42}}, agent)
	ctx := context.Background()

	_, err := u.Block(ctx, -100, 7, "Иван", 77, "24h")
	require.ErrorIs(t, err, errs.NoPermission, "в группе кнопки видят все — блокирует только админ")
	_, err = u.SkipBlock(ctx, -100, 7, "Иван", 77)
	require.ErrorIs(t, err, errs.NoPermission)
	_, err = u.Block(ctx, 999, 42, "Даурен", 77, "24h")
	require.ErrorIs(t, err, errs.NotAuthorized, "чат не разрешён")
	assert.Empty(t, agent.reqs, "отказ — без вызова агента")

	b, err := u.Block(ctx, -100, 42, "Даурен", 77, "24h")
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.7/32", b.Ip)
	_, err = u.SkipBlock(ctx, -100, 42, "Даурен", 77)
	require.NoError(t, err)
	assert.Equal(t, []*agentModel.BlockReq{
		{ConversationId: "tg:-100", NotificationId: 77, Action: "block", Duration: "24h", UserId: "42", UserName: "Даурен"},
		{ConversationId: "tg:-100", NotificationId: 77, Action: "skip", UserId: "42", UserName: "Даурен"},
	}, agent.reqs)

	agent.err = fmt.Errorf("%w: bot is not in BLOCK_CLIENTS", errs.NotAuthorized)
	_, err = u.Block(ctx, -100, 42, "Даурен", 77, "24h")
	require.ErrorIs(t, err, errs.Forbidden, "403 агента — боту не разрешено блокировать")
	assert.NotErrorIs(t, err, errs.NotAuthorized)

	agent.err = fmt.Errorf("%w: agent: status 503", errs.ServiceNA)
	_, err = u.Block(ctx, -100, 42, "Даурен", 77, "24h")
	require.ErrorIs(t, err, errs.ServiceNA)
}
