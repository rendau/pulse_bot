package notify

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_bot/internal/errs"
)

func TestCanManage(t *testing.T) {
	u := New(Config{Chats: []int64{-100, -100, 7}, AllowedUsers: []int64{42}}, nil)

	assert.Equal(t, []int64{-100, 7}, u.Chats())
	assert.True(t, u.CanManage(-100, 999), "в группе уведомлений — любой участник")
	assert.True(t, u.CanManage(42, 42), "в личке — из белого списка")
	assert.False(t, u.CanManage(999, 999))

	_, err := u.Mute(context.Background(), 999, 999, "", 1, "")
	require.ErrorIs(t, err, errs.NotAuthorized)
}
