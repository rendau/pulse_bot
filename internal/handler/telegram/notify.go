package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/samber/lo"

	"github.com/rendau/pulse_bot/internal/errs"
	agentModel "github.com/rendau/pulse_bot/internal/service/agent/model"
	notifyModel "github.com/rendau/pulse_bot/internal/usecase/notify/model"
	"github.com/rendau/pulse_bot/internal/util/tgmd"
)

// данные кнопок (callback_data, до 64 байт)
const (
	cbMute        = "mute"   // mute:<id уведомления>:<срок из muteOptions>
	cbUnmute      = "unmute" // unmute:<id приглушения>
	cbUnsubscribe = "unsub"  // unsub:<id подписки>
)

// muteOptions — кнопки приглушения под уведомлением: код в данных кнопки, срок для агента, подпись.
var muteOptions = []struct{ code, duration, label string }{
	{"1h", "1h", "1 ч"},
	{"1d", "1d", "сутки"},
	{"ever", "", "навсегда"},
}

var severityIcons = map[string]string{"critical": "🔴", "warning": "🟠", "info": "🔵"}

var kindLabels = map[string]string{"alert": "алерт", "deploy": "после выкатки", "logs": "ошибки в логах", "self": "сервис сообщает сам", "public": "публичный API"}

// kindPlurals — вид во множественном числе: «caravan · алерты».
var kindPlurals = map[string]string{"alert": "алерты", "deploy": "выкатки", "logs": "ошибки в логах", "self": "самоотчёты", "public": "публичный API"}

// Notifier — доставка ленты агента: раз в interval забирает новые уведомления каждого чата
// уведомлений, присылает неприглушённые с кнопками приглушения и подтверждает полученное.
type Notifier struct {
	notify   NotifyUsecaseI
	sender   SenderI
	interval time.Duration
	wg       sync.WaitGroup
}

func NewNotifier(notify NotifyUsecaseI, sender SenderI, interval time.Duration) *Notifier {
	return &Notifier{notify: notify, sender: sender, interval: interval}
}

func (n *Notifier) Start(ctx context.Context) {
	n.wg.Go(func() {
		ticker := time.NewTicker(n.interval)
		defer ticker.Stop()
		for {
			for _, chatId := range n.notify.Chats() {
				n.deliver(ctx, chatId)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (n *Notifier) Wait() {
	n.wg.Wait()
}

// deliver — новые уведомления чата: неприглушённые — в чат, затем подтверждение. Не
// отправилось из-за остановки — не подтверждаем (придёт снова); отказ Telegram (бота убрали из
// чата) — подтверждаем, чтобы не застрять на нём.
func (n *Notifier) deliver(ctx context.Context, chatId int64) {
	items, err := n.notify.Pending(ctx, chatId)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("notify: pending", "chat_id", chatId, "error", err)
		}
		return
	}

	var lastId int64
	var delivery notifyModel.Delivery
	for _, item := range items {
		switch {
		case item.NotSubscribed:
			delivery.NotSubscribed++
		case item.MutedBy != nil:
			delivery.Muted++
		default:
			if err = sendNotification(ctx, n.sender, chatId, item); err != nil {
				if ctx.Err() == nil {
					slog.Error("notify: send", "chat_id", chatId, "notification", item.Id, "error", err)
					delivery.Failed++
				}
			} else {
				delivery.Sent++
			}
		}
		if ctx.Err() != nil {
			break // остановка: неотправленное придёт снова
		}
		lastId = item.Id
	}
	if lastId == 0 {
		return
	}
	if err = n.notify.Ack(context.WithoutCancel(ctx), chatId, lastId, delivery); err != nil {
		slog.Warn("notify: ack", "chat_id", chatId, "error", err)
	}
}

// sendNotification — уведомление с кнопками приглушения; разметку не приняли — без неё.
func sendNotification(ctx context.Context, sender SenderI, chatId int64, n *agentModel.Notification) error {
	body := tgmd.Split(n.Text, chunkLimit-300)
	params := &bot.SendMessageParams{
		ChatID:      chatId,
		Text:        renderNotification(n, tgmd.ToHTML(lo.FirstOr(body, ""))),
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: muteKeyboard(n),
	}
	if _, err := sender.SendMessage(ctx, params); err != nil {
		slog.Warn("notify: send html failed, fallback to plain text", "chat_id", chatId, "error", err)
		params.Text, params.ParseMode = n.Title+"\n\n"+lo.FirstOr(body, ""), ""
		if _, err = sender.SendMessage(ctx, params); err != nil {
			return fmt.Errorf("SendMessage: %w", err)
		}
	}
	return nil
}

// renderNotification — «🟠 заголовок», разбор, внизу — сервис, вид и время.
func renderNotification(n *agentModel.Notification, bodyHtml string) string {
	footer := []string{html.EscapeString(n.Service), lo.CoalesceOrEmpty(kindLabels[n.Kind], n.Kind), n.At.Format("02.01 15:04")}
	if !n.Investigated {
		footer = append(footer, "без разбора")
	}
	return fmt.Sprintf("%s <b>%s</b>\n\n%s\n\n<i>%s</i>",
		lo.CoalesceOrEmpty(severityIcons[n.Severity], "🔔"), html.EscapeString(n.Title), bodyHtml, strings.Join(footer, " · "))
}

func muteKeyboard(n *agentModel.Notification) *models.InlineKeyboardMarkup {
	row := lo.Map(muteOptions, func(o struct{ code, duration, label string }, _ int) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{
			Text:         "🔕 " + o.label,
			CallbackData: fmt.Sprintf("%s:%d:%s", cbMute, n.Id, o.code),
		}
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
}

// callback — нажатие кнопки: приглушить сервис уведомления или вернуть приглушённое.
func (h *Handler) callback(ctx context.Context, sender SenderI, cq *models.CallbackQuery) {
	chat := callbackChat(cq)
	if chat == 0 || h.notify == nil {
		h.answerCallback(ctx, sender, cq, textCallbackStale)
		return
	}
	name := strings.TrimSpace(cq.From.FirstName + " " + cq.From.LastName)
	parts := strings.Split(cq.Data, ":")

	switch {
	case len(parts) == 3 && parts[0] == cbMute:
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		option, ok := lo.Find(muteOptions, func(o struct{ code, duration, label string }) bool { return o.code == parts[2] })
		if id <= 0 || !ok {
			h.answerCallback(ctx, sender, cq, textCallbackStale)
			return
		}
		m, err := h.notify.Mute(ctx, chat, cq.From.ID, name, id, option.duration)
		if err != nil {
			h.answerCallback(ctx, sender, cq, callbackError(err))
			return
		}
		h.answerCallback(ctx, sender, cq, "🔕 "+describeMute(m))
		h.sendText(ctx, sender, chat, fmt.Sprintf(textMuted, html.EscapeString(describeMute(m)), html.EscapeString(lo.CoalesceOrEmpty(name, "кто-то"))))

	case len(parts) == 2 && parts[0] == cbUnmute:
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		if err := h.notify.Unmute(ctx, chat, id); err != nil {
			h.answerCallback(ctx, sender, cq, callbackError(err))
			return
		}
		h.answerCallback(ctx, sender, cq, textUnmutedShort)
		h.sendText(ctx, sender, chat, fmt.Sprintf(textUnmuted, html.EscapeString(lo.CoalesceOrEmpty(name, "кто-то"))))

	case len(parts) == 2 && parts[0] == cbUnsubscribe:
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		if err := h.notify.Unsubscribe(ctx, chat, id); err != nil {
			h.answerCallback(ctx, sender, cq, callbackError(err))
			return
		}
		h.answerCallback(ctx, sender, cq, textUnsubscribedShort)
		h.sendText(ctx, sender, chat, fmt.Sprintf(textUnsubscribed, html.EscapeString(lo.CoalesceOrEmpty(name, "кто-то"))))

	default:
		h.answerCallback(ctx, sender, cq, textCallbackStale)
	}
}

// subscriptions — /subs: что приходит в чат, кнопки «убрать».
func (h *Handler) subscriptions(ctx context.Context, sender SenderI, msg *models.Message) {
	if h.notify == nil {
		h.reply(ctx, sender, msg, textNotifyOff)
		return
	}
	subs, err := h.notify.Subscriptions(ctx, msg.Chat.ID)
	if err != nil {
		if errors.Is(err, errs.NotAuthorized) {
			h.reply(ctx, sender, msg, fmt.Sprintf(textDenied, msg.From.ID))
			return
		}
		h.replyError(ctx, sender, msg, err)
		return
	}
	if len(subs) == 0 {
		h.reply(ctx, sender, msg, textNoSubscriptions)
		return
	}

	var b strings.Builder
	b.WriteString("🔔 <b>В этот чат приходит</b>\n")
	for _, sub := range subs {
		fmt.Fprintf(&b, "• %s", html.EscapeString(subscriptionSubject(sub)))
		if sub.CreatedBy != "" {
			fmt.Fprintf(&b, " <i>(%s)</i>", html.EscapeString(sub.CreatedBy))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + textSubscriptionsHint)

	buttons := lo.Map(subs, func(sub *agentModel.Subscription, _ int) []models.InlineKeyboardButton {
		return []models.InlineKeyboardButton{{
			Text:         "✖ Убрать: " + truncate(subscriptionSubject(sub), 40),
			CallbackData: fmt.Sprintf("%s:%d", cbUnsubscribe, sub.Id),
		}}
	})
	_, err = sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            b.String(),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID, AllowSendingWithoutReply: true},
		ReplyMarkup:     &models.InlineKeyboardMarkup{InlineKeyboard: buttons},
	})
	if err != nil {
		slog.Error("telegram: send subscriptions", "chat_id", msg.Chat.ID, "error", err)
	}
}

var severityLabels = map[string]string{"info": "любой важности", "warning": "warning и выше", "critical": "только critical"}

// subscriptionSubject — «caravan», «все сервисы · алерты · только critical».
func subscriptionSubject(sub *agentModel.Subscription) string {
	parts := []string{lo.CoalesceOrEmpty(sub.Service, "все сервисы")}
	if kind, ok := kindPlurals[sub.Kind]; ok {
		parts = append(parts, kind)
	}
	if sub.MinSeverity != "" && sub.MinSeverity != "info" {
		parts = append(parts, severityLabels[sub.MinSeverity])
	}
	return strings.Join(parts, " · ")
}

// muted — /muted: что приглушено в чате, что скрыто, кнопки «вернуть».
func (h *Handler) muted(ctx context.Context, sender SenderI, msg *models.Message) {
	if h.notify == nil {
		h.reply(ctx, sender, msg, textNotifyOff)
		return
	}
	mutes, muted, err := h.notify.Mutes(ctx, msg.Chat.ID)
	if err != nil {
		if errors.Is(err, errs.NotAuthorized) {
			h.reply(ctx, sender, msg, fmt.Sprintf(textDenied, msg.From.ID))
			return
		}
		h.replyError(ctx, sender, msg, err)
		return
	}
	if len(mutes) == 0 {
		h.reply(ctx, sender, msg, textNothingMuted)
		return
	}

	var b strings.Builder
	b.WriteString("🔕 <b>Приглушено в этом чате</b>\n")
	for _, m := range mutes {
		fmt.Fprintf(&b, "• %s", html.EscapeString(describeMute(m)))
		if m.Suppressed > 0 {
			fmt.Fprintf(&b, " — скрыто %d", m.Suppressed)
		}
		if m.CreatedBy != "" {
			fmt.Fprintf(&b, " <i>(%s)</i>", html.EscapeString(m.CreatedBy))
		}
		b.WriteString("\n")
	}
	if len(muted) > 0 {
		b.WriteString("\n<b>Последние скрытые</b>\n")
		for _, n := range muted {
			fmt.Fprintf(&b, "• %s %s\n", n.At.Format("02.01 15:04"), html.EscapeString(n.Title))
		}
	}

	buttons := lo.Map(mutes, func(m *agentModel.Mute, _ int) []models.InlineKeyboardButton {
		return []models.InlineKeyboardButton{{
			Text:         "🔔 Вернуть: " + truncate(muteSubject(m), 40),
			CallbackData: fmt.Sprintf("%s:%d", cbUnmute, m.Id),
		}}
	})
	_, err = sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            b.String(),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID, AllowSendingWithoutReply: true},
		ReplyMarkup:     &models.InlineKeyboardMarkup{InlineKeyboard: buttons},
	})
	if err != nil {
		slog.Error("telegram: send mutes", "chat_id", msg.Chat.ID, "error", err)
	}
}

// muteSubject — что приглушено: «caravan», «caravan · алерты», «все уведомления».
func muteSubject(m *agentModel.Mute) string {
	subject := lo.CoalesceOrEmpty(m.Service, "все уведомления")
	if kind, ok := kindPlurals[m.Kind]; ok {
		subject += " · " + kind
	}
	if m.Key != "" {
		subject += " · " + m.Key
	}
	return subject
}

// describeMute — «caravan до 27.09 18:00» или «caravan навсегда».
func describeMute(m *agentModel.Mute) string {
	if m.Until == nil {
		return muteSubject(m) + " навсегда"
	}
	return muteSubject(m) + " до " + m.Until.Format("02.01 15:04")
}

func callbackChat(cq *models.CallbackQuery) int64 {
	switch {
	case cq.Message.Message != nil:
		return cq.Message.Message.Chat.ID
	case cq.Message.InaccessibleMessage != nil:
		return cq.Message.InaccessibleMessage.Chat.ID
	}
	return 0
}

func callbackError(err error) string {
	switch {
	case errors.Is(err, errs.NotAuthorized):
		return textCallbackDenied
	case errors.Is(err, errs.ObjectNotFound):
		return textCallbackStale
	}
	slog.Error("notify: callback", "error", err)
	return textCallbackFailed
}

func (h *Handler) answerCallback(ctx context.Context, sender SenderI, cq *models.CallbackQuery, text string) {
	if _, err := sender.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID, Text: text}); err != nil {
		slog.Warn("telegram: answer callback", "error", err)
	}
}

// sendText — сообщение в чат (HTML), не ответом.
func (h *Handler) sendText(ctx context.Context, sender SenderI, chatId int64, text string) {
	if _, err := sender.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatId, Text: text, ParseMode: models.ParseModeHTML}); err != nil {
		slog.Error("telegram: send message", "chat_id", chatId, "error", err)
	}
}
