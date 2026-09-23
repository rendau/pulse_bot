package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	localConstant "github.com/mechta-market/pulse_bot/internal/service/agent/service/constant"
	llmModel "github.com/mechta-market/pulse_bot/internal/service/llm/model"
	pulseModel "github.com/mechta-market/pulse_bot/internal/service/pulse/model"
)

// finalReserve — время, которое остаётся на финальный ответ модели: после
// loopDeadline инструменты больше не вызываются.
const finalReserve = time.Minute

// Config — ограничители разбора.
type Config struct {
	MaxToolCalls int
	Timeout      time.Duration
}

type Service struct {
	cfg   Config
	llm   llmI
	pulse pulseI
	now   func() time.Time
}

func New(cfg Config, llm llmI, pulse pulseI) *Service {
	return &Service{cfg: cfg, llm: llm, pulse: pulse, now: time.Now}
}

func (s *Service) Run(ctx context.Context, req *agentModel.Req) (*agentModel.Result, error) {
	started := s.now()

	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	loopDeadline := started.Add(s.cfg.Timeout - min(finalReserve, s.cfg.Timeout/4))

	catalog, err := s.pulse.Catalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("pulse.Catalog: %w", err)
	}

	llmReq := &llmModel.Request{
		System:   localConstant.SystemPrompt(catalog.Instructions),
		Messages: buildMessages(req, started),
		Tools:    lo.Map(catalog.Tools, encodeTool),
	}

	result := &agentModel.Result{}
	defer func() { metricRunSteps.Observe(float64(result.Steps)) }()

	for {
		if !llmReq.NoTools && s.now().After(loopDeadline) {
			result.Incomplete = agentModel.IncompleteTimeout
			llmReq.NoTools = true
		}

		resp, err := s.complete(ctx, llmReq)
		if err != nil {
			return nil, err
		}
		result.Steps++
		result.Usage.Add(resp.Usage)

		if len(resp.ToolCalls) == 0 || llmReq.NoTools {
			result.Answer = strings.TrimSpace(resp.Text)
			if resp.Incomplete != "" && result.Incomplete == "" {
				result.Incomplete = agentModel.IncompleteOutput
			}
			return result, nil
		}

		llmReq.State = resp.State

		// лимит вызовов: на запрошенные вызовы отвечаем отказом и просим закончить
		if result.ToolCalls+len(resp.ToolCalls) > s.cfg.MaxToolCalls {
			result.Incomplete = agentModel.IncompleteToolCalls
			llmReq.NoTools = true
			llmReq.ToolResults = lo.Map(resp.ToolCalls, func(c llmModel.ToolCall, _ int) llmModel.ToolResult {
				return llmModel.ToolResult{CallId: c.Id, Output: localConstant.ToolSkipped}
			})
			continue
		}

		toolCtx, toolCancel := context.WithDeadline(ctx, loopDeadline)
		llmReq.ToolResults = s.callTools(toolCtx, resp.ToolCalls)
		toolCancel()
		result.ToolCalls += len(resp.ToolCalls)
	}
}

// complete — шаг модели с метриками.
func (s *Service) complete(ctx context.Context, req *llmModel.Request) (*llmModel.Response, error) {
	provider := s.llm.Name()
	started := s.now()

	resp, err := s.llm.Complete(ctx, req)
	metricLlmRequestDuration.WithLabelValues(provider).Observe(s.now().Sub(started).Seconds())
	if err != nil {
		metricLlmRequests.WithLabelValues(provider, statusError).Inc()
		return nil, fmt.Errorf("llm.Complete: %w", err)
	}
	metricLlmRequests.WithLabelValues(provider, statusOk).Inc()

	metricLlmTokens.WithLabelValues(provider, "input").Add(float64(resp.Usage.InputTokens))
	metricLlmTokens.WithLabelValues(provider, "cached").Add(float64(resp.Usage.CachedTokens))
	metricLlmTokens.WithLabelValues(provider, "output").Add(float64(resp.Usage.OutputTokens))
	metricLlmTokens.WithLabelValues(provider, "reasoning").Add(float64(resp.Usage.ReasoningTokens))

	return resp, nil
}

// callTools выполняет вызовы шага параллельно; ошибка вызова не роняет разбор,
// а уходит модели текстом.
func (s *Service) callTools(ctx context.Context, calls []llmModel.ToolCall) []llmModel.ToolResult {
	results := make([]llmModel.ToolResult, len(calls))

	var g errgroup.Group
	for i, call := range calls {
		g.Go(func() error {
			results[i] = llmModel.ToolResult{CallId: call.Id, Output: s.callTool(ctx, call)}
			return nil
		})
	}
	_ = g.Wait()

	return results
}

func (s *Service) callTool(ctx context.Context, call llmModel.ToolCall) string {
	started := s.now()

	res, err := s.pulse.Call(ctx, call.Name, call.Arguments)
	metricToolCallDuration.WithLabelValues(call.Name).Observe(s.now().Sub(started).Seconds())

	switch {
	case err != nil:
		metricToolCalls.WithLabelValues(call.Name, statusError).Inc()
		slog.Warn("pulse tool call failed", "tool", call.Name, "error", err)
		return localConstant.ToolErrorPrefix + err.Error()
	case res.IsError:
		metricToolCalls.WithLabelValues(call.Name, statusToolError).Inc()
		slog.Debug("pulse tool error", "tool", call.Name, "arguments", call.Arguments, "text", res.Text)
		return localConstant.ToolErrorPrefix + res.Text
	default:
		metricToolCalls.WithLabelValues(call.Name, statusOk).Inc()
		slog.Debug("pulse tool call", "tool", call.Name, "arguments", call.Arguments, "bytes", strconv.Itoa(len(res.Text)))
		return res.Text
	}
}

func buildMessages(req *agentModel.Req, now time.Time) []llmModel.Message {
	messages := lo.FlatMap(req.History, func(t agentModel.Turn, _ int) []llmModel.Message {
		return []llmModel.Message{
			{Role: llmModel.RoleUser, Text: t.Question},
			{Role: llmModel.RoleAssistant, Text: t.Answer},
		}
	})

	return append(messages, llmModel.Message{
		Role: llmModel.RoleUser,
		Text: fmt.Sprintf(localConstant.QuestionTemplate, now.UTC().Format(time.RFC3339), req.Question),
	})
}

func encodeTool(t pulseModel.Tool, _ int) llmModel.ToolDef {
	return llmModel.ToolDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}
}
