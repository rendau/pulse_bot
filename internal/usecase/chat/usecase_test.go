package chat

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_bot/internal/domain/dialog/repo/mem"
	dialogServiceP "github.com/mechta-market/pulse_bot/internal/domain/dialog/service"
	"github.com/mechta-market/pulse_bot/internal/errs"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	"github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

type fakeAgent struct {
	reqs    []*agentModel.Req
	result  *agentModel.Result
	started chan struct{}
	release chan struct{}
}

func (f *fakeAgent) Run(_ context.Context, req *agentModel.Req) (*agentModel.Result, error) {
	f.reqs = append(f.reqs, req)
	if f.started != nil {
		f.started <- struct{}{}
		<-f.release
	}
	return f.result, nil
}

func newUsecase(agent *fakeAgent) *Usecase {
	dialog := dialogServiceP.New(dialogServiceP.Config{MaxTurns: 10, Ttl: time.Hour}, mem.New())
	return New(Config{AllowedUsers: []int64{42}}, dialog, agent)
}

func TestAsk(t *testing.T) {
	ctx := context.Background()
	agent := &fakeAgent{result: &agentModel.Result{Answer: "всё ок"}}
	uc := newUsecase(agent)

	ans, err := uc.Ask(ctx, &model.Question{ChatId: 1, UserId: 42, Text: "  что с caravan?  "})
	require.NoError(t, err)
	assert.Equal(t, &model.Answer{Text: "всё ок"}, ans)

	// второй вопрос идёт с историей первого
	_, err = uc.Ask(ctx, &model.Question{ChatId: 1, UserId: 42, Text: "а логи?"})
	require.NoError(t, err)
	require.Len(t, agent.reqs, 2)
	assert.Equal(t, []agentModel.Turn{{Question: "что с caravan?", Answer: "всё ок"}}, agent.reqs[1].History)

	// после /reset истории нет
	require.NoError(t, uc.Reset(ctx, 1, 42))
	_, err = uc.Ask(ctx, &model.Question{ChatId: 1, UserId: 42, Text: "ещё"})
	require.NoError(t, err)
	assert.Empty(t, agent.reqs[2].History)
}

func TestAsk_Denied(t *testing.T) {
	uc := newUsecase(&fakeAgent{})

	_, err := uc.Ask(context.Background(), &model.Question{ChatId: 1, UserId: 7, Text: "q"})
	assert.ErrorIs(t, err, errs.NotAuthorized)

	assert.ErrorIs(t, uc.Reset(context.Background(), 1, 7), errs.NotAuthorized)
}

func TestAsk_Empty(t *testing.T) {
	_, err := newUsecase(&fakeAgent{}).Ask(context.Background(), &model.Question{ChatId: 1, UserId: 42, Text: " "})
	assert.ErrorIs(t, err, errs.InvalidRequest)
}

func TestAsk_BusyPerChat(t *testing.T) {
	agent := &fakeAgent{
		result:  &agentModel.Result{Answer: "ok"},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	uc := newUsecase(agent)
	ctx := context.Background()

	done := make(chan error)
	go func() {
		_, err := uc.Ask(ctx, &model.Question{ChatId: 1, UserId: 42, Text: "первый"})
		done <- err
	}()
	<-agent.started

	// тот же чат занят
	_, err := uc.Ask(ctx, &model.Question{ChatId: 1, UserId: 42, Text: "второй"})
	assert.ErrorIs(t, err, errs.Busy)

	close(agent.release)
	require.NoError(t, <-done)

	// освободился
	agent.started = nil
	_, err = uc.Ask(ctx, &model.Question{ChatId: 1, UserId: 42, Text: "третий"})
	assert.NoError(t, err)
}
