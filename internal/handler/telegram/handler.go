package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mechta-market/pulse_bot/internal/errs"
	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
	"github.com/mechta-market/pulse_bot/internal/util/tgmd"
)

const (
	// typingInterval — «печатает…» в Telegram гаснет через ~5 секунд, продлеваем чаще
	typingInterval = 4 * time.Second

	// chunkLimit — запас под лимит Telegram (4096): разметка может чуть удлинить текст
	chunkLimit = tgmd.MessageLimit - 196

	// sendTimeout — на отправку ответа после отмены разбора (перезапуск бота)
	sendTimeout = 10 * time.Second

	errorTextLimit = 300
)

// keyboard — постоянная кнопка сброса под полем ввода (вместо набора /reset руками).
// Telegram держит её, пока не придёт другая клавиатура, поэтому достаточно
// прикреплять к приветствию, сбросу и ответам.
var keyboard = &models.ReplyKeyboardMarkup{
	Keyboard:       [][]models.KeyboardButton{{{Text: buttonReset}}},
	IsPersistent:   true,
	ResizeKeyboard: true,
}

// Handler — транспорт Telegram: принимает сообщения из личных чатов и отвечает.
// Сообщения обрабатываются параллельно (разные чаты не ждут друг друга);
// Wait дожидается незаконченных при остановке.
type Handler struct {
	chat ChatUsecaseI
	wg   sync.WaitGroup
}

func New(chat ChatUsecaseI) *Handler {
	return &Handler{chat: chat}
}

// Handle — обработчик обновлений для bot.WithDefaultHandler (с WithNotAsyncHandlers:
// горутину на сообщение запускает сам Handler, чтобы дождаться её в Wait).
func (h *Handler) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.From == nil {
		return
	}

	// v1: только личные сообщения, в группах бот молчит
	if msg.Chat.Type != models.ChatTypePrivate {
		return
	}

	h.wg.Go(func() {
		h.process(ctx, b, msg)
	})
}

// Wait дожидается обработки принятых сообщений.
func (h *Handler) Wait() {
	h.wg.Wait()
}

func (h *Handler) process(ctx context.Context, sender SenderI, msg *models.Message) {
	chatId, userId := msg.Chat.ID, msg.From.ID
	text := strings.TrimSpace(msg.Text)

	switch {
	case text == "":
		h.reply(ctx, sender, msg, textNotText)
		return
	case isCommand(text, "/start"), isCommand(text, "/help"):
		if !h.chat.Allowed(userId) {
			h.reply(ctx, sender, msg, fmt.Sprintf(textDenied, userId))
			return
		}
		h.replyWithKeyboard(ctx, sender, msg, textWelcome)
		return
	case isCommand(text, "/reset"), text == buttonReset:
		if err := h.chat.Reset(ctx, chatId, userId); err != nil {
			h.replyError(ctx, sender, msg, err)
			return
		}
		h.replyWithKeyboard(ctx, sender, msg, textReset)
		return
	}

	answer, err := h.askWithTyping(ctx, sender, &chatModel.Question{ChatId: chatId, UserId: userId, Text: text})
	if err != nil {
		h.replyError(ctx, sender, msg, err)
		return
	}

	h.sendAnswer(ctx, sender, msg, answer)
}

// askWithTyping держит «печатает…», пока идёт разбор.
func (h *Handler) askWithTyping(ctx context.Context, sender SenderI, q *chatModel.Question) (*chatModel.Answer, error) {
	typingCtx, stopTyping := context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(typingInterval)
		defer ticker.Stop()

		for {
			_, _ = sender.SendChatAction(typingCtx, &bot.SendChatActionParams{ChatID: q.ChatId, Action: models.ChatActionTyping})

			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
			}
		}
	})

	answer, err := h.chat.Ask(ctx, q)

	stopTyping()
	wg.Wait()

	return answer, err
}

func (h *Handler) sendAnswer(ctx context.Context, sender SenderI, msg *models.Message, answer *chatModel.Answer) {
	text := answer.Text
	if text == "" {
		text = textEmptyAnswer
	}
	if note, ok := incompleteNotes[answer.Incomplete]; ok {
		text += "\n\n*" + note + "*"
	}

	for i, chunk := range tgmd.Split(text, chunkLimit) {
		params := &bot.SendMessageParams{
			ChatID:    msg.Chat.ID,
			Text:      tgmd.ToHTML(chunk),
			ParseMode: models.ParseModeHTML,
		}
		if i == 0 {
			params.ReplyParameters = &models.ReplyParameters{MessageID: msg.ID, AllowSendingWithoutReply: true}
			params.ReplyMarkup = keyboard
		}

		if _, err := sender.SendMessage(ctx, params); err != nil {
			// разметку не приняли — отправляем как есть, без неё
			slog.Warn("telegram: send html failed, fallback to plain text", "chat_id", msg.Chat.ID, "error", err)
			params.Text, params.ParseMode = chunk, ""
			if _, err = sender.SendMessage(ctx, params); err != nil {
				slog.Error("telegram: send message", "chat_id", msg.Chat.ID, "error", err)
				return
			}
		}
	}
}

func (h *Handler) replyError(ctx context.Context, sender SenderI, msg *models.Message, err error) {
	switch {
	case errors.Is(err, errs.NotAuthorized):
		h.reply(ctx, sender, msg, fmt.Sprintf(textDenied, msg.From.ID))
	case errors.Is(err, errs.Busy):
		h.reply(ctx, sender, msg, textBusy)
	case errors.Is(err, errs.InvalidRequest):
		h.reply(ctx, sender, msg, textNotText)
	case ctx.Err() != nil:
		// бот останавливается: разбор отменён вместе с корневым контекстом
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
		defer cancel()
		h.reply(sendCtx, sender, msg, textShuttingDown)
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("question timed out", "chat_id", msg.Chat.ID, "error", err)
		h.reply(ctx, sender, msg, textTimeout)
	default:
		slog.Error("question failed", "chat_id", msg.Chat.ID, "error", err)
		h.reply(ctx, sender, msg, fmt.Sprintf(textError, html.EscapeString(truncate(err.Error(), errorTextLimit))))
	}
}

// reply отправляет готовый HTML.
func (h *Handler) reply(ctx context.Context, sender SenderI, msg *models.Message, text string) {
	h.send(ctx, sender, msg, text, nil)
}

// replyWithKeyboard — reply с кнопкой сброса.
func (h *Handler) replyWithKeyboard(ctx context.Context, sender SenderI, msg *models.Message, text string) {
	h.send(ctx, sender, msg, text, keyboard)
}

func (h *Handler) send(ctx context.Context, sender SenderI, msg *models.Message, text string, markup models.ReplyMarkup) {
	_, err := sender.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            text,
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID, AllowSendingWithoutReply: true},
		ReplyMarkup:     markup,
	})
	if err != nil {
		slog.Error("telegram: send message", "chat_id", msg.Chat.ID, "error", err)
	}
}

// isCommand — text это команда cmd (в т.ч. с суффиксом @имя_бота или аргументами).
func isCommand(text, cmd string) bool {
	head, _, _ := strings.Cut(text, " ")
	head, _, _ = strings.Cut(head, "@")
	return head == cmd
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}
