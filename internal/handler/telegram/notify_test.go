package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_bot/internal/errs"
	agentModel "github.com/rendau/pulse_bot/internal/service/agent/model"
	chatModel "github.com/rendau/pulse_bot/internal/usecase/chat/model"
	notifyModel "github.com/rendau/pulse_bot/internal/usecase/notify/model"
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
	// блокировка: что вернуть и с чем вызвали (duration skip — не блокировать)
	block      *agentModel.Block
	blockErr   error
	blockCalls []string
}

func (f *fakeNotify) Block(_ context.Context, _, _ int64, _ string, notificationId int64, duration string) (*agentModel.Block, error) {
	f.blockCalls = append(f.blockCalls, fmt.Sprintf("%d:%s", notificationId, duration))
	return f.block, f.blockErr
}

func (f *fakeNotify) SkipBlock(_ context.Context, _, _ int64, _ string, notificationId int64) (*agentModel.Block, error) {
	f.blockCalls = append(f.blockCalls, fmt.Sprintf("%d:skip", notificationId))
	return f.block, f.blockErr
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
	rows := msg.ReplyMarkup.(*models.InlineKeyboardMarkup).InlineKeyboard
	require.Len(t, rows, 1, "не атака — без кнопок блокировки")
	buttons := rows[0]
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

func TestSecurityLabels(t *testing.T) {
	n := &agentModel.Notification{Kind: "security", Key: "129.226.55.176", Severity: "critical", Investigated: true,
		Title: "атака с 129.226.55.176: <перебор>", At: time.Date(2026, 11, 23, 19, 40, 0, 0, time.UTC)}
	text := renderNotification(n, "тело")
	assert.Contains(t, text, "<i>129.226.55.176 · атака из интернета · 23.11 19:40</i>", "у атаки вместо сервиса — адрес")
	assert.Contains(t, text, "&lt;перебор&gt;", "заголовок экранирован")

	assert.Equal(t, "атаки из интернета · 129.226.55.176", muteSubject(&agentModel.Mute{Kind: "security", Key: "129.226.55.176"}))
	assert.Equal(t, "все уведомления", muteSubject(&agentModel.Mute{}))
	assert.Equal(t, "caravan · алерты", muteSubject(&agentModel.Mute{Service: "caravan", Kind: "alert"}))

	assert.Equal(t, "атаки из интернета", subscriptionSubject(&agentModel.Subscription{Kind: "security"}))
	assert.Equal(t, "атаки из интернета · только critical", subscriptionSubject(&agentModel.Subscription{Kind: "security", MinSeverity: "critical"}))
	assert.Equal(t, "всё, кроме атак из интернета", subscriptionSubject(&agentModel.Subscription{}))
	assert.Equal(t, "все сервисы · алерты", subscriptionSubject(&agentModel.Subscription{Kind: "alert"}))
}

func TestNotificationKeyboard(t *testing.T) {
	rows := notificationKeyboard(&agentModel.Notification{Id: 77, Kind: "security", Key: "203.0.113.7"}).InlineKeyboard
	require.Len(t, rows, 2, "у атаки — ряд приглушения и ряд блокировки")
	assert.Equal(t, "mute:77:1h", rows[0][0].CallbackData)
	assert.Equal(t, []string{"⛔ Блок 24 ч", "⛔ Блок 7 дней", "Не блокировать"},
		[]string{rows[1][0].Text, rows[1][1].Text, rows[1][2].Text})
	assert.Equal(t, []string{"blk:77:1d", "blk:77:7d", "nblk:77"},
		[]string{rows[1][0].CallbackData, rows[1][1].CallbackData, rows[1][2].CallbackData}, "в данных id, не адрес")

	assert.Len(t, notificationKeyboard(&agentModel.Notification{Id: 77, Kind: "alert"}).InlineKeyboard, 1)

	// callback_data — до 64 байт
	for _, b := range blockRow(9_223_372_036_854_775_807) {
		assert.LessOrEqual(t, len(b.CallbackData), 64)
	}
}

func TestParseBlockChoice(t *testing.T) {
	for data, want := range map[string]blockChoice{
		"blk:77:1d": {notificationId: 77, duration: "24h"},
		"blk:77:7d": {notificationId: 77, duration: "168h"},
		"nblk:77":   {notificationId: 77, skip: true},
	} {
		got, ok := parseBlockChoice(data)
		assert.True(t, ok, data)
		assert.Equal(t, want, got, data)
	}
	for _, data := range []string{"blk:77", "blk:77:30d", "blk:x:1d", "blk:-1:1d", "blk:0:1d", "nblk:77:1d", "nblk:", "nblk", "mute:77:1d", "blk:77:1d:x"} {
		_, ok := parseBlockChoice(data)
		assert.False(t, ok, data)
	}
}

// blockQuery — нажатие под уведомлением 77 (сообщение 500) в группе.
func blockQuery(data string) *models.CallbackQuery {
	cq := callbackQuery(group, data)
	cq.Message.Message.ID = 500
	return cq
}

func TestCallbackBlock(t *testing.T) {
	until := time.Date(2026, 11, 25, 19, 40, 0, 0, time.Local)
	ip := "203.0.113.7/32"

	cases := []struct {
		name      string
		data      string
		block     *agentModel.Block
		call      string
		answer    string
		post      string
		dropsRow  bool
		withAlert bool
	}{
		{
			name:  "заблокирован на сутки",
			data:  "blk:77:1d",
			block: &agentModel.Block{NotificationId: 77, Ip: ip, Action: "block", Until: &until, ByName: "Иван"},
			call:  "77:24h", answer: "⛔ Заблокирован до 25.11 19:40",
			post:     "⛔ Иван заблокировал <code>203.0.113.7/32</code> до 25.11 19:40",
			dropsRow: true,
		},
		{
			name:  "уже заблокирован в ruto человеком — без срока",
			data:  "blk:77:7d",
			block: &agentModel.Block{Ip: ip, Action: "block", ByName: "Иван"},
			call:  "77:168h", answer: "⛔ Заблокирован без срока",
			post:     "⛔ Иван заблокировал <code>203.0.113.7/32</code> без срока",
			dropsRow: true,
		},
		{
			name:  "не блокировать",
			data:  "nblk:77",
			block: &agentModel.Block{Ip: ip, Action: "skip", ByName: "Иван"},
			call:  "77:skip", answer: "Не блокирую",
			post:     "Иван: не блокировать <code>203.0.113.7/32</code>",
			dropsRow: true,
		},
		{
			name:  "повтор: уже решил другой админ",
			data:  "blk:77:1d",
			block: &agentModel.Block{Ip: ip, Action: "block", Until: &until, ByName: "Асель", Repeat: true},
			call:  "77:24h", answer: "Уже заблокирован: Асель, до 25.11 19:40",
			post:     "⛔ <code>203.0.113.7/32</code> уже заблокирован: Асель, до 25.11 19:40",
			dropsRow: true,
		},
		{
			name:  "повтор: уже решено не блокировать",
			data:  "blk:77:1d",
			block: &agentModel.Block{Ip: ip, Action: "skip", ByName: "Асель", Repeat: true},
			call:  "77:24h", answer: "Уже решено не блокировать: Асель",
			post:     "<code>203.0.113.7/32</code>: уже решено не блокировать (Асель)",
			dropsRow: true,
		},
		{
			name:  "ruto отказал — кнопки остаются",
			data:  "blk:77:1d",
			block: &agentModel.Block{Ip: ip, Action: "block", Refused: "адрес <в белом списке>"},
			call:  "77:24h", answer: "ruto отказал: адрес <в белом списке>",
			post: "ruto отказал: адрес &lt;в белом списке&gt;",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			notify, sender := &fakeNotify{block: c.block}, &fakeSender{}
			New(&fakeChat{}, notify, 1, "pulse_bot").callback(context.Background(), sender, blockQuery(c.data))

			assert.Equal(t, []string{c.call}, notify.blockCalls)
			assert.Equal(t, []string{c.answer}, sender.answers)
			require.Len(t, sender.sent, 1)
			assert.Equal(t, c.post, sender.sent[0].Text)
			if !c.dropsRow {
				assert.Empty(t, sender.edits)
				return
			}
			require.Len(t, sender.edits, 1)
			edit := sender.edits[0]
			assert.Equal(t, group, edit.ChatID)
			assert.Equal(t, 500, edit.MessageID)
			rows := edit.ReplyMarkup.(*models.InlineKeyboardMarkup).InlineKeyboard
			require.Len(t, rows, 1, "ряд блокировки убран, приглушение осталось")
			assert.Equal(t, "mute:77:1h", rows[0][0].CallbackData)
		})
	}
}

func TestCallbackBlockErrors(t *testing.T) {
	for err, want := range map[error]string{
		errs.NoPermission:  textBlockAdminsOnly,
		errs.NotAuthorized: textCallbackDenied,
		fmt.Errorf("%w: agent.Block: forbidden", errs.Forbidden): textBlockForbidden,
		fmt.Errorf("agent.Block: %w", errs.ObjectNotFound):       textBlockStale,
		fmt.Errorf("agent.Block: %w", errs.ServiceNA):            textBlockServiceNA,
		fmt.Errorf("agent.Block: %w", errs.InvalidRequest):       textCallbackFailed,
	} {
		notify, sender := &fakeNotify{blockErr: err}, &fakeSender{}
		New(&fakeChat{}, notify, 1, "pulse_bot").callback(context.Background(), sender, blockQuery("blk:77:1d"))

		assert.Equal(t, []string{want}, sender.answers, err.Error())
		assert.Equal(t, []bool{true}, sender.alerts, "отказ — окном")
		assert.Empty(t, sender.sent, "в чат ничего")
		assert.Empty(t, sender.edits, "кнопки на месте")
	}

	notify, sender := &fakeNotify{}, &fakeSender{}
	New(&fakeChat{}, notify, 1, "pulse_bot").callback(context.Background(), sender, blockQuery("blk:77:30d"))
	assert.Empty(t, notify.blockCalls, "испорченные данные — агенту не идём")
	assert.Equal(t, []string{textCallbackStale}, sender.answers)
}

func TestCallbackTextLimit(t *testing.T) {
	notify := &fakeNotify{block: &agentModel.Block{Refused: strings.Repeat("я", 1000)}}
	sender := &fakeSender{}
	New(&fakeChat{}, notify, 1, "pulse_bot").callback(context.Background(), sender, blockQuery("blk:77:1d"))

	require.Len(t, sender.answers, 1)
	assert.LessOrEqual(t, len([]rune(sender.answers[0])), 200, "answerCallbackQuery — до 200 символов")
	require.Len(t, sender.sent, 1)
	assert.Less(t, len([]rune(sender.sent[0].Text)), 600, "отказ в чате урезан")
}
