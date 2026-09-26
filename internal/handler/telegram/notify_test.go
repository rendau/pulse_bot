package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_bot/internal/errs"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

const group = int64(-100500)

type fakeNotify struct {
	pending  []*agentModel.Notification
	acked    int64
	sent     int
	muted    int
	mutedIds []int64
	unmuted  []int64
	mutes    []*agentModel.Mute
}

func (f *fakeNotify) Chats() []int64                 { return []int64{group} }
func (f *fakeNotify) NotifyChat(chatId int64) bool   { return chatId == group }
func (f *fakeNotify) CanManage(chatId, _ int64) bool { return chatId == group }
func (f *fakeNotify) Pending(context.Context, int64) ([]*agentModel.Notification, error) {
	return f.pending, nil
}

func (f *fakeNotify) Ack(_ context.Context, _, lastId int64, sent, muted, _ int) error {
	f.acked, f.sent, f.muted = lastId, sent, muted
	return nil
}

func (f *fakeNotify) Mute(_ context.Context, chatId, _ int64, _ string, notificationId int64, duration string) (*agentModel.Mute, error) {
	if chatId != group {
		return nil, errs.NotAuthorized
	}
	f.mutedIds = append(f.mutedIds, notificationId)
	m := &agentModel.Mute{Id: 7, Service: "caravan"}
	if duration != "" {
		m.Until = new(time.Date(2026, 9, 27, 18, 0, 0, 0, time.FixedZone("+05", 5*3600)))
	}
	return m, nil
}

func (f *fakeNotify) Unmute(_ context.Context, _, _, muteId int64) error {
	f.unmuted = append(f.unmuted, muteId)
	return nil
}

func (f *fakeNotify) Mutes(context.Context, int64, int64) ([]*agentModel.Mute, []*agentModel.Notification, error) {
	return f.mutes, nil, nil
}

var at = time.Date(2026, 9, 26, 17, 35, 0, 0, time.FixedZone("+05", 5*3600))

func TestNotifierDeliver(t *testing.T) {
	notify := &fakeNotify{pending: []*agentModel.Notification{
		{Id: 41, At: at, Kind: "deploy", Service: "caravan", Severity: "warning", Title: "caravan: после выкатки 8% сбоев", Text: "- **сбои** с 0 до 8%", Investigated: true},
		{Id: 42, At: at, Kind: "alert", Service: "notifire", Title: "notifire: алерт", MutedBy: new(int64(3))},
	}}
	sender := &fakeSender{}
	NewNotifier(notify, sender, time.Minute).deliver(context.Background(), group)

	require.Len(t, sender.sent, 1, "приглушённое не присылается")
	msg := sender.sent[0]
	assert.Equal(t, group, msg.ChatID)
	assert.Contains(t, msg.Text, "🟠 <b>caravan: после выкатки 8% сбоев</b>")
	assert.Contains(t, msg.Text, "<b>сбои</b>")
	assert.Contains(t, msg.Text, "<i>caravan · после выкатки · 26.09 17:35</i>")
	buttons := msg.ReplyMarkup.(*models.InlineKeyboardMarkup).InlineKeyboard[0]
	assert.Equal(t, "mute:41:1h", buttons[0].CallbackData)
	assert.Equal(t, "mute:41:ever", buttons[2].CallbackData)

	assert.Equal(t, int64(42), notify.acked, "подтверждено и приглушённое")
	assert.Equal(t, 1, notify.sent)
	assert.Equal(t, 1, notify.muted)
}

func callbackQuery(chatId int64, data string) *models.CallbackQuery {
	return &models.CallbackQuery{
		ID: "cq", From: models.User{ID: 7, FirstName: "Иван"}, Data: data,
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: chatId}}},
	}
}

func TestCallbackMute(t *testing.T) {
	notify, sender := &fakeNotify{}, &fakeSender{}
	h := New(&fakeChat{}, notify, 1)

	h.callback(context.Background(), sender, callbackQuery(group, "mute:41:1d"))
	assert.Equal(t, []int64{41}, notify.mutedIds)
	assert.Equal(t, []string{"🔕 caravan до 27.09 18:00"}, sender.answers)
	require.Len(t, sender.sent, 1, "чат видит, кто приглушил")
	assert.Contains(t, sender.sent[0].Text, "caravan до 27.09 18:00 (Иван)")

	h.callback(context.Background(), sender, callbackQuery(group, "unmute:7"))
	assert.Equal(t, []int64{7}, notify.unmuted)

	h.callback(context.Background(), sender, callbackQuery(555, "mute:41:1h"))
	assert.Equal(t, textCallbackDenied, sender.answers[len(sender.answers)-1], "чужой чат — нельзя")

	h.callback(context.Background(), sender, callbackQuery(group, "mute:41:week"))
	assert.Equal(t, textCallbackStale, sender.answers[len(sender.answers)-1])
}

func TestMutedCommand(t *testing.T) {
	notify := &fakeNotify{mutes: []*agentModel.Mute{{Id: 7, Service: "caravan", Kind: "alert", Suppressed: 3, CreatedBy: "Иван"}}}
	sender := &fakeSender{}
	msg := message(7, "/muted")
	msg.Chat = models.Chat{ID: group, Type: models.ChatTypeSupergroup}
	New(&fakeChat{}, notify, 1).process(context.Background(), sender, msg)

	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0].Text, "caravan · алерты навсегда — скрыто 3 <i>(Иван)</i>")
	buttons := sender.sent[0].ReplyMarkup.(*models.InlineKeyboardMarkup).InlineKeyboard
	assert.Equal(t, "unmute:7", buttons[0][0].CallbackData)

	notify.mutes = nil
	New(&fakeChat{}, notify, 1).process(context.Background(), sender, msg)
	assert.Contains(t, sender.sent[1].Text, "Ничего не приглушено")
}

// askChat — запоминает вопрос.
type askChat struct {
	fakeChat
	question string
}

func (f *askChat) Ask(_ context.Context, q *chatModel.Question) (*chatModel.Answer, error) {
	f.question = q.Text
	return &chatModel.Answer{Text: "ок"}, nil
}

func TestGroupReplyToNotification(t *testing.T) {
	chat := &askChat{}
	h := New(chat, &fakeNotify{}, 1)

	msg := message(7, "заглуши это до понедельника")
	msg.Chat = models.Chat{ID: group, Type: models.ChatTypeSupergroup}
	assert.Empty(t, h.repliedTo(msg), "не ответ боту — в группе молчим")

	msg.ReplyToMessage = &models.Message{From: &models.User{ID: 1, IsBot: true}, Text: "🟠 caravan: после выкатки 8% сбоев"}
	assert.NotEmpty(t, h.repliedTo(msg))
	h.process(context.Background(), &fakeSender{}, msg)
	assert.True(t, strings.HasPrefix(chat.question, "Ответ на сообщение бота:\n«🟠 caravan: после выкатки 8% сбоев»"), chat.question)
	assert.True(t, strings.HasSuffix(chat.question, "заглуши это до понедельника"))

	msg.ReplyToMessage.From.ID = 2
	assert.Empty(t, h.repliedTo(msg), "ответ другому боту — не нам")
}
