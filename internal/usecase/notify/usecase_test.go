package notify

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_bot/internal/errs"
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
