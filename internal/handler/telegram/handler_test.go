package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_bot/internal/errs"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

type fakeChat struct {
	answer   *chatModel.Answer
	err      error
	resets   int
	evalText string
	evalErr  error
	evalOnly []string
}

func (f *fakeChat) Admin(userId int64) bool { return userId == 42 }

func (f *fakeChat) Eval(_ context.Context, _ int64, only []string) (string, error) {
	f.evalOnly = only
	return f.evalText, f.evalErr
}

func (f *fakeChat) Allowed(userId int64) bool { return userId == 42 }

func (f *fakeChat) Ask(context.Context, *chatModel.Question) (*chatModel.Answer, error) {
	return f.answer, f.err
}

func (f *fakeChat) Reset(context.Context, int64, int64) error {
	f.resets++
	return nil
}

// fakeSender запоминает отправленные сообщения; failHtml — Telegram «не принял» разметку.
type fakeSender struct {
	mu       sync.Mutex
	sent     []*bot.SendMessageParams
	photos   []*bot.SendPhotoParams
	answers  []string
	failHtml bool
}

func (f *fakeSender) SendPhoto(_ context.Context, p *bot.SendPhotoParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.photos = append(f.photos, p)
	return &models.Message{}, nil
}

func (f *fakeSender) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failHtml && p.ParseMode == models.ParseModeHTML {
		return nil, errors.New("Bad Request: can't parse entities")
	}
	f.sent = append(f.sent, p)
	return &models.Message{}, nil
}

func (f *fakeSender) SendChatAction(context.Context, *bot.SendChatActionParams) (bool, error) {
	return true, nil
}

func (f *fakeSender) AnswerCallbackQuery(_ context.Context, p *bot.AnswerCallbackQueryParams) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, p.Text)
	return true, nil
}

func message(userId int64, text string) *models.Message {
	return &models.Message{
		ID:   10,
		From: &models.User{ID: userId},
		Chat: models.Chat{ID: 100, Type: models.ChatTypePrivate},
		Text: text,
	}
}

func TestProcess_Answer(t *testing.T) {
	sender := &fakeSender{}
	h := New(&fakeChat{answer: &chatModel.Answer{Text: "**caravan** в порядке", Incomplete: agentModel.IncompleteTimeout}}, nil, 0)

	h.process(context.Background(), sender, message(42, "что с caravan?"))

	require.Len(t, sender.sent, 1)
	got := sender.sent[0]
	assert.Equal(t, models.ParseModeHTML, got.ParseMode)
	assert.True(t, strings.HasPrefix(got.Text, "<b>caravan</b> в порядке"))
	assert.Contains(t, got.Text, "<i>"+incompleteNotes[agentModel.IncompleteTimeout]+"</i>")
	assert.Equal(t, 10, got.ReplyParameters.MessageID)
}

func TestProcess_LongAnswerSplit(t *testing.T) {
	sender := &fakeSender{}
	paragraph := strings.Repeat("слово ", 500) // ~3000 символов
	h := New(&fakeChat{answer: &chatModel.Answer{Text: paragraph + "\n\n" + paragraph}}, nil, 0)

	h.process(context.Background(), sender, message(42, "q"))

	require.Len(t, sender.sent, 2)
	assert.NotNil(t, sender.sent[0].ReplyParameters)
	assert.Nil(t, sender.sent[1].ReplyParameters)
}

func TestProcess_PlainTextFallback(t *testing.T) {
	sender := &fakeSender{failHtml: true}
	h := New(&fakeChat{answer: &chatModel.Answer{Text: "**ответ**"}}, nil, 0)

	h.process(context.Background(), sender, message(42, "q"))

	require.Len(t, sender.sent, 1)
	assert.Empty(t, sender.sent[0].ParseMode)
	assert.Equal(t, "**ответ**", sender.sent[0].Text)
}

func TestProcess_Replies(t *testing.T) {
	tests := []struct {
		name   string
		chat   *fakeChat
		userId int64
		text   string
		want   string
	}{
		{"start allowed", &fakeChat{}, 42, "/start", textWelcome},
		{"start denied shows id", &fakeChat{}, 7, "/start", "Ваш Telegram ID: <code>7</code>"},
		{"reset", &fakeChat{}, 42, "/reset@pulse_bot", textReset},
		{"reset button", &fakeChat{}, 42, buttonReset, textReset},
		{"denied question", &fakeChat{err: errs.NotAuthorized}, 7, "q", "<code>7</code>"},
		{"busy", &fakeChat{err: errs.Busy}, 42, "q", textBusy},
		{"not text", &fakeChat{}, 42, "", textNotText},
		{"timeout", &fakeChat{err: context.DeadlineExceeded}, 42, "q", textTimeout},
		{"error escaped", &fakeChat{err: errors.New("pulse <down>")}, 42, "q", "pulse &lt;down&gt;"},
		{"empty answer", &fakeChat{answer: &chatModel.Answer{}}, 42, "q", textEmptyAnswer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			New(tt.chat, nil, 0).process(context.Background(), sender, message(tt.userId, tt.text))

			require.Len(t, sender.sent, 1)
			assert.Contains(t, sender.sent[0].Text, tt.want)
		})
	}
}

func TestProcess_Keyboard(t *testing.T) {
	tests := []struct {
		name   string
		chat   *fakeChat
		userId int64
		text   string
		want   bool
	}{
		{"welcome", &fakeChat{}, 42, "/start", true},
		{"reset", &fakeChat{}, 42, buttonReset, true},
		{"answer", &fakeChat{answer: &chatModel.Answer{Text: "ок"}}, 42, "q", true},
		{"denied", &fakeChat{}, 7, "/start", false},
		{"busy", &fakeChat{err: errs.Busy}, 42, "q", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			New(tt.chat, nil, 0).process(context.Background(), sender, message(tt.userId, tt.text))

			require.Len(t, sender.sent, 1)
			if tt.want {
				assert.Equal(t, keyboard, sender.sent[0].ReplyMarkup)
			} else {
				assert.Nil(t, sender.sent[0].ReplyMarkup)
			}
		})
	}
}

func TestProcess_ShuttingDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sender := &fakeSender{}
	New(&fakeChat{err: context.Canceled}, nil, 0).process(ctx, sender, message(42, "q"))

	require.Len(t, sender.sent, 1)
	assert.Equal(t, textShuttingDown, sender.sent[0].Text)
}

func TestHandle_IgnoresGroups(t *testing.T) {
	h := New(&fakeChat{}, nil, 0)
	msg := message(42, "q")
	msg.Chat.Type = models.ChatTypeGroup

	// в группе обработчик не запускается вовсе (bot не нужен)
	h.Handle(context.Background(), nil, &models.Update{Message: msg})
	h.Wait()
}

func TestProcess_Charts(t *testing.T) {
	sender := &fakeSender{}
	h := New(&fakeChat{answer: &chatModel.Answer{Text: "память в норме", Charts: []agentModel.Chart{
		{Title: "Память caravan", Png: []byte("png1")}, {Title: "Ошибки", Png: []byte("png2")},
	}}}, nil, 0)
	h.process(context.Background(), sender, &models.Message{ID: 1, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, From: &models.User{ID: 7}, Text: "память caravan?"})

	require.Len(t, sender.sent, 1, "сначала текст")
	require.Len(t, sender.photos, 2)
	assert.Equal(t, "Память caravan", sender.photos[0].Caption)
	upload, ok := sender.photos[1].Photo.(*models.InputFileUpload)
	require.True(t, ok)
	assert.Equal(t, "chart-2.png", upload.Filename)
}

func TestProcess_Eval(t *testing.T) {
	msg := func(userId int64, text string) *models.Message {
		return &models.Message{ID: 1, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, From: &models.User{ID: userId}, Text: text}
	}

	chat := &fakeChat{evalText: "✅ a  1.0s\n❌ b <x>\n\nИтого: 1/2"}
	sender := &fakeSender{}
	New(chat, nil, 0).process(context.Background(), sender, msg(42, "/eval a b"))

	require.Len(t, sender.sent, 2)
	assert.Equal(t, textEvalStarted, sender.sent[0].Text)
	assert.Equal(t, "<pre>✅ a  1.0s\n❌ b &lt;x&gt;\n\nИтого: 1/2</pre>", sender.sent[1].Text, "таблица моноширинно, HTML экранирован")
	assert.Equal(t, []string{"a", "b"}, chat.evalOnly)

	// не админ
	sender = &fakeSender{}
	New(chat, nil, 0).process(context.Background(), sender, msg(7, "/eval"))
	require.Len(t, sender.sent, 1)
	assert.Equal(t, textEvalDenied, sender.sent[0].Text)

	// прогон уже идёт
	sender = &fakeSender{}
	New(&fakeChat{evalErr: errs.Busy}, nil, 0).process(context.Background(), sender, msg(42, "/eval"))
	require.Len(t, sender.sent, 2)
	assert.Equal(t, textEvalBusy, sender.sent[1].Text)
}

func TestSplitLines(t *testing.T) {
	text := strings.Repeat("строка таблицы\n", 10)
	chunks := splitLines(text, 60)
	require.Greater(t, len(chunks), 1)
	for _, c := range chunks {
		assert.LessOrEqual(t, len(c), 60)
	}
	assert.Equal(t, strings.TrimRight(text, "\n"), strings.Join(chunks, "\n"))
}
