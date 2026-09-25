package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_bot/internal/constant"
	dialogModel "github.com/mechta-market/pulse_bot/internal/domain/dialog/model"
	"github.com/mechta-market/pulse_bot/internal/errs"
	"github.com/mechta-market/pulse_bot/internal/infra/metrics"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	"github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

var (
	metricQuestions      *prometheus.CounterVec
	metricAnswerDuration prometheus.Histogram
)

func init() {
	metricQuestions = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "question_total",
		Help: "Вопросы по исходу: answered, incomplete, denied, busy, error.",
	}, []string{"outcome"})

	metricAnswerDuration = metrics.Factory.NewHistogram(prometheus.HistogramOpts{
		Name:    "answer_duration_seconds",
		Help:    "Время от вопроса до ответа (разобранные вопросы).",
		Buckets: []float64{5, 10, 20, 40, 60, 120, 180, 300},
	})
}

// Config — доступ к боту.
type Config struct {
	AllowedUsers []int64

	// AllowAll — без белого списка: отладочная ручка, доступ к ней проверяет
	// транспорт (bearer-токен).
	AllowAll bool
}

type Usecase struct {
	allowAll bool
	allowed  map[int64]struct{}
	dialog   DialogServiceI
	agent    AgentI

	mu   sync.Mutex
	busy map[int64]struct{} // чаты, где идёт разбор
}

func New(cfg Config, dialog DialogServiceI, agent AgentI) *Usecase {
	return &Usecase{
		allowAll: cfg.AllowAll,
		allowed:  lo.SliceToMap(cfg.AllowedUsers, func(id int64) (int64, struct{}) { return id, struct{}{} }),
		dialog:   dialog,
		agent:    agent,
		busy:     map[int64]struct{}{},
	}
}

// Allowed — есть ли пользователь в белом списке.
func (u *Usecase) Allowed(userId int64) bool {
	if u.allowAll {
		return true
	}
	_, ok := u.allowed[userId]
	return ok
}

// Ask разбирает вопрос. Ошибки: errs.NotAuthorized — пользователя нет в белом
// списке; errs.Busy — в чате уже идёт разбор; errs.InvalidRequest — пустой вопрос.
func (u *Usecase) Ask(ctx context.Context, q *model.Question) (*model.Answer, error) {
	if !u.Allowed(q.UserId) {
		metricQuestions.WithLabelValues(constant.OutcomeDenied).Inc()
		return nil, errs.NotAuthorized
	}

	text := strings.TrimSpace(q.Text)
	if text == "" {
		return nil, errs.InvalidRequest
	}

	if !u.lock(q.ChatId) {
		metricQuestions.WithLabelValues(constant.OutcomeBusy).Inc()
		return nil, errs.Busy
	}
	defer u.unlock(q.ChatId)

	started := time.Now()

	answer, err := u.ask(ctx, q.ChatId, text)
	if err != nil {
		metricQuestions.WithLabelValues(constant.OutcomeError).Inc()
		return nil, err
	}

	metricAnswerDuration.Observe(time.Since(started).Seconds())
	metricQuestions.WithLabelValues(lo.Ternary(answer.Incomplete == "", constant.OutcomeAnswered, constant.OutcomeIncomplete)).Inc()

	return answer, nil
}

func (u *Usecase) ask(ctx context.Context, chatId int64, text string) (*model.Answer, error) {
	history, err := u.dialog.History(ctx, chatId)
	if err != nil {
		return nil, fmt.Errorf("dialog.History: %w", err)
	}

	result, err := u.agent.Run(ctx, &agentModel.Req{
		History: lo.Map(history, func(t *dialogModel.Turn, _ int) agentModel.Turn {
			return agentModel.Turn{Question: t.Question, Answer: t.Answer}
		}),
		Question: text,
	})
	if err != nil {
		return nil, fmt.Errorf("agent.Run: %w", err)
	}

	slog.Info("question answered",
		"chat_id", chatId,
		"steps", result.Steps,
		"tool_calls", result.ToolCalls,
		"incomplete", result.Incomplete,
		"input_tokens", result.Usage.InputTokens,
		"cached_tokens", result.Usage.CachedTokens,
		"output_tokens", result.Usage.OutputTokens,
		"charts", len(result.Charts),
	)

	// пустой ответ в историю не кладём: он только собьёт следующий вопрос
	if result.Answer != "" {
		if err = u.dialog.Append(ctx, chatId, text, result.Answer); err != nil {
			return nil, fmt.Errorf("dialog.Append: %w", err)
		}
	}

	return &model.Answer{
		Text:       result.Answer,
		Incomplete: result.Incomplete,
		Charts:     result.Charts,
		Steps:      result.Steps,
		ToolCalls:  result.ToolCalls,
		Usage:      result.Usage,
		Trace:      result.Trace,
	}, nil
}

// Reset забывает историю чата.
func (u *Usecase) Reset(ctx context.Context, chatId, userId int64) error {
	if !u.Allowed(userId) {
		return errs.NotAuthorized
	}

	if err := u.dialog.Reset(ctx, chatId); err != nil {
		return fmt.Errorf("dialog.Reset: %w", err)
	}

	return nil
}

func (u *Usecase) lock(chatId int64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, ok := u.busy[chatId]; ok {
		return false
	}
	u.busy[chatId] = struct{}{}
	return true
}

func (u *Usecase) unlock(chatId int64) {
	u.mu.Lock()
	defer u.mu.Unlock()

	delete(u.busy, chatId)
}
