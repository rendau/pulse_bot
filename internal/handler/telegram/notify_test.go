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
	notifyModel "github.com/mechta-market/pulse_bot/internal/usecase/notify/model"
)

const group = int64(-100500)

type fakeNotify struct {
	pending      []*agentModel.Notification
	acked        int64
	delivery     notifyModel.Delivery
	mutedIds     []int64
	unmuted      []int64
	mutes        []*agentModel.Mute
	subs         []*agentModel.Subscription
	unsubscribed []int64
}

func (f *fakeNotify) Subscriptions(context.Context, int64) ([]*agentModel.Subscription, error) {
	return f.subs, nil
}

func (f *fakeNotify) Unsubscribe(_ context.Context, _, id int64) error {
	f.unsubscribed = append(f.unsubscribed, id)
	return nil
}

func (f *fakeNotify) Chats() []int64              { return []int64{group} }
func (f *fakeNotify) CanManage(chatId int64) bool { return chatId == group }
func (f *fakeNotify) Pending(context.Context, int64) ([]*agentModel.Notification, error) {
	return f.pending, nil
}

func (f *fakeNotify) Ack(_ context.Context, _, lastId int64, delivery notifyModel.Delivery) error {
	f.acked, f.delivery = lastId, delivery
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

func (f *fakeNotify) Unmute(_ context.Context, _, muteId int64) error {
	f.unmuted = append(f.unmuted, muteId)
	return nil
}

func (f *fakeNotify) Mutes(context.Context, int64) ([]*agentModel.Mute, []*agentModel.Notification, error) {
	return f.mutes, nil, nil
}

var at = time.Date(2026, 9, 26, 17, 35, 0, 0, time.FixedZone("+05", 5*3600))

func TestNotifierDeliver(t *testing.T) {
	notify := &fakeNotify{pending: []*agentModel.Notification{
		{Id: 41, At: at, Kind: "deploy", Service: "caravan", Severity: "warning", Title: "caravan: после выкатки 8% сбоев", Text: "- **сбои** с 0 до 8%", Investigated: true},
		{Id: 42, At: at, Kind: "alert", Service: "notifire", Title: "notifire: алерт", MutedBy: new(int64(3))},
		{Id: 43, At: at, Kind: "alert", Service: "receipt", Title: "receipt: алерт", NotSubscribed: true},
	}}
	sender := &fakeSender{}
	NewNotifier(notify, sender, time.Minute).deliver(context.Background(), group)

	require.Len(t, sender.sent, 1, "приглушённое и не по подпискам не присылается")
	msg := sender.sent[0]
	assert.Equal(t, group, msg.ChatID)
	assert.Contains(t, msg.Text, "🟠 <b>caravan: после выкатки 8% сбоев</b>")
	assert.Contains(t, msg.Text, "<b>сбои</b>")
	assert.Contains(t, msg.Text, "<i>caravan · после выкатки · 26.09 17:35</i>")
	buttons := msg.ReplyMarkup.(*models.InlineKeyboardMarkup).InlineKeyboard[0]
	assert.Equal(t, "mute:41:1h", buttons[0].CallbackData)
	assert.Equal(t, "mute:41:ever", buttons[2].CallbackData)

	assert.Equal(t, int64(43), notify.acked, "подтверждено и скрытое")
	assert.Equal(t, notifyModel.Delivery{Sent: 1, Muted: 1, NotSubscribed: 1}, notify.delivery)
}

func callbackQuery(chatId int64, data string) *models.CallbackQuery {
	return &models.CallbackQuery{
		ID: "cq", From: models.User{ID: 7, FirstName: "Иван"}, Data: data,
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: chatId}}},
	}
}

func TestCallbackMute(t *testing.T) {
	notify, sender := &fakeNotify{}, &fakeSender{}
	h := New(&fakeChat{}, notify, 1, "pulse_bot")

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
	New(&fakeChat{}, notify, 1, "pulse_bot").process(context.Background(), sender, msg)

	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0].Text, "caravan · алерты навсегда — скрыто 3 <i>(Иван)</i>")
	buttons := sender.sent[0].ReplyMarkup.(*models.InlineKeyboardMarkup).InlineKeyboard
	assert.Equal(t, "unmute:7", buttons[0][0].CallbackData)

	notify.mutes = nil
	New(&fakeChat{}, notify, 1, "pulse_bot").process(context.Background(), sender, msg)
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
	h := New(chat, &fakeNotify{}, 1, "pulse_bot")

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

func TestSubsCommand(t *testing.T) {
	notify := &fakeNotify{subs: []*agentModel.Subscription{
		{Id: 3, Service: "caravan", CreatedBy: "Иван"},
		{Id: 4, Kind: "alert", MinSeverity: "critical"},
	}}
	sender := &fakeSender{}
	msg := message(7, "/subs@pulse_bot")
	msg.Chat = models.Chat{ID: group, Type: models.ChatTypeSupergroup}
	h := New(&fakeChat{}, notify, 1, "pulse_bot")
	h.process(context.Background(), sender, msg)

	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0].Text, "• caravan <i>(Иван)</i>")
	assert.Contains(t, sender.sent[0].Text, "• все сервисы · алерты · только critical")
	buttons := sender.sent[0].ReplyMarkup.(*models.InlineKeyboardMarkup).InlineKeyboard
	assert.Equal(t, "unsub:3", buttons[0][0].CallbackData)

	h.callback(context.Background(), sender, callbackQuery(group, "unsub:3"))
	assert.Equal(t, []int64{3}, notify.unsubscribed)

	notify.subs = nil
	h.process(context.Background(), sender, msg)
	assert.Contains(t, sender.sent[len(sender.sent)-1].Text, "Уведомления в этот чат не приходят")
}

func TestGroupAddressed(t *testing.T) {
	chat := &askChat{}
	h := New(chat, &fakeNotify{}, 1, "Pulse_Bot")
	groupMsg := func(text string) *models.Message {
		m := message(7, text)
		m.Chat = models.Chat{ID: group, Type: models.ChatTypeSupergroup}
		return m
	}

	assert.True(t, h.addressed(groupMsg("@pulse_bot что с caravan?")), "упоминание, регистр не важен")
	assert.True(t, h.addressed(groupMsg("/subs")))
	assert.True(t, h.addressed(groupMsg("/subs@pulse_bot")))
	assert.False(t, h.addressed(groupMsg("/subs@other_bot")), "команда другому боту")
	assert.False(t, h.addressed(groupMsg("обсуждаем caravan")), "обычная переписка группы")
	assert.False(t, h.addressed(groupMsg("@pulse_botnik привет")), "другое имя")

	h.process(context.Background(), &fakeSender{}, groupMsg("@Pulse_Bot что с caravan?"))
	assert.Equal(t, "что с caravan?", chat.question, "упоминание убрано из вопроса")
}
