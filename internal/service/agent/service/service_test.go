package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	localConstant "github.com/mechta-market/pulse_bot/internal/service/agent/service/constant"
	llmModel "github.com/mechta-market/pulse_bot/internal/service/llm/model"
	pulseModel "github.com/mechta-market/pulse_bot/internal/service/pulse/model"
)

// fakeLlm отвечает заранее заданными шагами и запоминает запросы.
type fakeLlm struct {
	steps    []*llmModel.Response
	requests []llmModel.Request
}

func (f *fakeLlm) Name() string { return "fake" }

func (f *fakeLlm) Complete(_ context.Context, req *llmModel.Request) (*llmModel.Response, error) {
	f.requests = append(f.requests, *req)
	if len(f.steps) == 0 {
		return nil, errors.New("unexpected step")
	}
	resp := f.steps[0]
	f.steps = f.steps[1:]
	return resp, nil
}

type fakePulse struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakePulse) Catalog(context.Context) (*pulseModel.Catalog, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &pulseModel.Catalog{
		Tools:        []pulseModel.Tool{{Name: "resolve_service", InputSchema: map[string]any{"type": "object"}}},
		Instructions: "сначала resolve_service",
	}, nil
}

func (f *fakePulse) Call(_ context.Context, name, arguments string) (*pulseModel.CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+" "+arguments)

	switch name {
	case "broken":
		return nil, errors.New("connection refused")
	case "bad_args":
		return &pulseModel.CallResult{Text: "unknown service", IsError: true}, nil
	}
	return &pulseModel.CallResult{Text: `{"ok":true}`}, nil
}

func toolStep(state string, calls ...llmModel.ToolCall) *llmModel.Response {
	return &llmModel.Response{ToolCalls: calls, State: state, Usage: llmModel.Usage{InputTokens: 100, OutputTokens: 10}}
}

func textStep(text string) *llmModel.Response {
	return &llmModel.Response{Text: text, Usage: llmModel.Usage{InputTokens: 100, CachedTokens: 80, OutputTokens: 20}}
}

func newService(llm *fakeLlm, pulse *fakePulse, maxToolCalls int) *Service {
	return New(Config{MaxToolCalls: maxToolCalls, Timeout: 5 * time.Minute}, llm, pulse)
}

func TestRun_ToolLoop(t *testing.T) {
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1",
			llmModel.ToolCall{Id: "c1", Name: "resolve_service", Arguments: `{"query":"caravan"}`},
			llmModel.ToolCall{Id: "c2", Name: "bad_args", Arguments: `{}`},
			llmModel.ToolCall{Id: "c3", Name: "broken", Arguments: `{}`},
		),
		textStep("  caravan в порядке  "),
	}}
	pulse := &fakePulse{}

	res, err := newService(llm, pulse, 20).Run(context.Background(), &agentModel.Req{
		History:  []agentModel.Turn{{Question: "что с кластером?", Answer: "всё ок"}},
		Question: "что с caravan?",
	})
	require.NoError(t, err)

	assert.Equal(t, "caravan в порядке", res.Answer)
	assert.Empty(t, res.Incomplete)
	assert.Equal(t, 2, res.Steps)
	assert.Equal(t, 3, res.ToolCalls)
	assert.Equal(t, llmModel.Usage{InputTokens: 200, CachedTokens: 80, OutputTokens: 30}, res.Usage)

	// первый шаг: системный промпт с instructions pulse, история + вопрос со временем
	first := llm.requests[0]
	assert.Contains(t, first.System, "сначала resolve_service")
	require.Len(t, first.Messages, 3)
	assert.Equal(t, llmModel.Message{Role: llmModel.RoleUser, Text: "что с кластером?"}, first.Messages[0])
	assert.Equal(t, llmModel.Message{Role: llmModel.RoleAssistant, Text: "всё ок"}, first.Messages[1])
	assert.True(t, strings.HasSuffix(first.Messages[2].Text, "что с caravan?"))
	assert.Contains(t, first.Messages[2].Text, "Текущее время: ")
	require.Len(t, first.Tools, 1)
	assert.Nil(t, first.State)

	// второй шаг: состояние адаптера и результаты вызовов в порядке вызовов
	second := llm.requests[1]
	assert.Equal(t, "s1", second.State)
	assert.False(t, second.NoTools)
	assert.Equal(t, []llmModel.ToolResult{
		{CallId: "c1", Output: `{"ok":true}`},
		{CallId: "c2", Output: localConstant.ToolErrorPrefix + "unknown service"},
		{CallId: "c3", Output: localConstant.ToolErrorPrefix + "connection refused"},
	}, second.ToolResults)
}

func TestRun_ToolCallsLimit(t *testing.T) {
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1", llmModel.ToolCall{Id: "c1", Name: "resolve_service"}),
		toolStep("s2",
			llmModel.ToolCall{Id: "c2", Name: "resolve_service"},
			llmModel.ToolCall{Id: "c3", Name: "resolve_service"},
		),
		textStep("что успел"),
	}}
	pulse := &fakePulse{}

	res, err := newService(llm, pulse, 2).Run(context.Background(), &agentModel.Req{Question: "q"})
	require.NoError(t, err)

	assert.Equal(t, "что успел", res.Answer)
	assert.Equal(t, agentModel.IncompleteToolCalls, res.Incomplete)
	assert.Equal(t, 1, res.ToolCalls)
	assert.Len(t, pulse.calls, 1)

	// вызовы сверх лимита не выполняются, модель просят закончить без инструментов
	last := llm.requests[2]
	assert.True(t, last.NoTools)
	assert.Equal(t, "s2", last.State)
	assert.Equal(t, []llmModel.ToolResult{
		{CallId: "c2", Output: localConstant.ToolSkipped},
		{CallId: "c3", Output: localConstant.ToolSkipped},
	}, last.ToolResults)
}

func TestRun_Timeout(t *testing.T) {
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1", llmModel.ToolCall{Id: "c1", Name: "resolve_service"}),
		textStep("по времени"),
	}}
	svc := newService(llm, &fakePulse{}, 20)

	// часы: старт, затем сразу после loopDeadline
	start := time.Now()
	ticks := 0
	svc.now = func() time.Time {
		ticks++
		if ticks <= 2 {
			return start
		}
		return start.Add(5 * time.Minute)
	}

	res, err := svc.Run(context.Background(), &agentModel.Req{Question: "q"})
	require.NoError(t, err)

	assert.Equal(t, "по времени", res.Answer)
	assert.Equal(t, agentModel.IncompleteTimeout, res.Incomplete)
	assert.True(t, llm.requests[1].NoTools)
}

func TestRun_OutputTruncated(t *testing.T) {
	resp := textStep("обрыв")
	resp.Incomplete = "max_output_tokens"
	llm := &fakeLlm{steps: []*llmModel.Response{resp}}

	res, err := newService(llm, &fakePulse{}, 20).Run(context.Background(), &agentModel.Req{Question: "q"})
	require.NoError(t, err)

	assert.Equal(t, agentModel.IncompleteOutput, res.Incomplete)
}

func TestRun_Errors(t *testing.T) {
	t.Run("pulse unavailable", func(t *testing.T) {
		_, err := newService(&fakeLlm{}, &fakePulse{err: errors.New("dial tcp: refused")}, 20).
			Run(context.Background(), &agentModel.Req{Question: "q"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pulse.Catalog")
	})

	t.Run("llm failed", func(t *testing.T) {
		_, err := newService(&fakeLlm{}, &fakePulse{}, 20).
			Run(context.Background(), &agentModel.Req{Question: "q"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "llm.Complete")
	})
}
