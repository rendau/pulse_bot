package chat

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_bot/internal/errs"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	"github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

type fakeAgent struct {
	reqs     []*agentModel.AskReq
	resets   []string
	answer   *agentModel.Answer
	err      error
	started  chan struct{}
	release  chan struct{}
	evalOnly []string
}

func (f *fakeAgent) Ask(_ context.Context, req *agentModel.AskReq) (*agentModel.Answer, error) {
	f.reqs = append(f.reqs, req)
	if f.started != nil {
		f.started <- struct{}{}
		<-f.release
	}
	return f.answer, f.err
}

func (f *fakeAgent) Eval(_ context.Context, only []string) (string, error) {
	f.evalOnly = only
	return "таблица", nil
}

func (f *fakeAgent) Reset(_ context.Context, conversationId string) error {
	f.resets = append(f.resets, conversationId)
	return nil
}

func TestAsk(t *testing.T) {
	agent := &fakeAgent{answer: &agentModel.Answer{Text: "всё ок", Charts: []agentModel.Chart{{Title: "c"}}}}
	uc := New(Config{AllowedUsers: []int64{42}}, agent)

	ans, err := uc.Ask(context.Background(), &model.Question{ChatId: 1, UserId: 42, UserName: "Даурен", Text: "  что с caravan?  "})
	require.NoError(t, err)
	assert.Equal(t, "всё ок", ans.Text)
	assert.Len(t, ans.Charts, 1)
	assert.Equal(t, &agentModel.AskReq{ConversationId: "tg:1", UserId: "42", UserName: "Даурен", Question: "что с caravan?"}, agent.reqs[0])

	require.NoError(t, uc.Reset(context.Background(), 1, 42))
	assert.Equal(t, []string{"tg:1"}, agent.resets)
}

func TestAsk_Denied(t *testing.T) {
	uc := New(Config{AllowedUsers: []int64{42}}, &fakeAgent{})

	_, err := uc.Ask(context.Background(), &model.Question{ChatId: 1, UserId: 7, Text: "q"})
	require.ErrorIs(t, err, errs.NotAuthorized)
	require.ErrorIs(t, uc.Reset(context.Background(), 1, 7), errs.NotAuthorized)

	_, err = New(Config{AllowedUsers: []int64{42}}, &fakeAgent{}).Ask(context.Background(), &model.Question{ChatId: 1, UserId: 42, Text: "  "})
	require.ErrorIs(t, err, errs.InvalidRequest)
}

func TestAsk_Busy(t *testing.T) {
	agent := &fakeAgent{answer: &agentModel.Answer{Text: "ok"}, started: make(chan struct{}), release: make(chan struct{})}
	uc := New(Config{AllowedUsers: []int64{42}}, agent)

	done := make(chan error)
	go func() {
		_, err := uc.Ask(context.Background(), &model.Question{ChatId: 1, UserId: 42, Text: "первый"})
		done <- err
	}()
	<-agent.started

	_, err := uc.Ask(context.Background(), &model.Question{ChatId: 1, UserId: 42, Text: "второй"})
	require.ErrorIs(t, err, errs.Busy)

	close(agent.release)
	require.NoError(t, <-done)

	// агент ответил «занято» (вопрос из другого процесса бота) — тоже Busy
	agent = &fakeAgent{err: errs.Busy}
	_, err = New(Config{AllowedUsers: []int64{42}}, agent).Ask(context.Background(), &model.Question{ChatId: 1, UserId: 42, Text: "q"})
	require.ErrorIs(t, err, errs.Busy)
}

func TestEval(t *testing.T) {
	agent := &fakeAgent{}
	uc := New(Config{AllowedUsers: []int64{42, 7}, AdminUsers: []int64{42}}, agent)

	text, err := uc.Eval(context.Background(), 42, []string{"order-found"})
	require.NoError(t, err)
	assert.Equal(t, "таблица", text)
	assert.Equal(t, []string{"order-found"}, agent.evalOnly)

	_, err = uc.Eval(context.Background(), 7, nil)
	require.ErrorIs(t, err, errs.NotAuthorized, "в белом списке, но не админ")
}
