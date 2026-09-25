package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_bot/internal/constant"
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
	// AdminUsers — кому можно /eval
	AdminUsers []int64
	// AskTimeout, EvalTimeout — сколько ждать ответ агента и прогон эталонов
	AskTimeout  time.Duration
	EvalTimeout time.Duration
}

type Usecase struct {
	cfg     Config
	allowed map[int64]struct{}
	admins  map[int64]struct{}
	agent   AgentI

	mu   sync.Mutex
	busy map[int64]struct{} // чаты, где идёт разбор
}

func New(cfg Config, agent AgentI) *Usecase {
	return &Usecase{
		cfg:     cfg,
		allowed: lo.SliceToMap(cfg.AllowedUsers, func(id int64) (int64, struct{}) { return id, struct{}{} }),
		admins:  lo.SliceToMap(cfg.AdminUsers, func(id int64) (int64, struct{}) { return id, struct{}{} }),
		agent:   agent,
		busy:    map[int64]struct{}{},
	}
}

// Allowed — есть ли пользователь в белом списке.
func (u *Usecase) Allowed(userId int64) bool {
	_, ok := u.allowed[userId]
	return ok
}

// Ask разбирает вопрос (разбор и история беседы — в pulse_agent). Ошибки:
// errs.NotAuthorized — пользователя нет в белом списке; errs.Busy — в чате уже идёт
// разбор; errs.InvalidRequest — пустой вопрос.
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

	if u.cfg.AskTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, u.cfg.AskTimeout)
		defer cancel()
	}

	answer, err := u.agent.Ask(ctx, &agentModel.AskReq{
		ConversationId: conversationId(q.ChatId),
		UserId:         strconv.FormatInt(q.UserId, 10),
		UserName:       q.UserName,
		Question:       text,
	})
	if err != nil {
		metricQuestions.WithLabelValues(lo.Ternary(errors.Is(err, errs.Busy), constant.OutcomeBusy, constant.OutcomeError)).Inc()
		return nil, fmt.Errorf("agent.Ask: %w", err)
	}

	metricAnswerDuration.Observe(time.Since(started).Seconds())
	metricQuestions.WithLabelValues(lo.Ternary(answer.Incomplete == "", constant.OutcomeAnswered, constant.OutcomeIncomplete)).Inc()
	slog.Info("question answered", "chat_id", q.ChatId, "incomplete", answer.Incomplete, "charts", len(answer.Charts))

	return &model.Answer{Text: answer.Text, Incomplete: answer.Incomplete, Charts: answer.Charts}, nil
}

// Admin — может ли пользователь запускать /eval.
func (u *Usecase) Admin(userId int64) bool {
	_, ok := u.admins[userId]
	return ok
}

// Eval — прогон эталонных вопросов агента (только админы): таблица текстом.
func (u *Usecase) Eval(ctx context.Context, userId int64, only []string) (string, error) {
	if !u.Admin(userId) {
		return "", errs.NotAuthorized
	}
	if u.cfg.EvalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, u.cfg.EvalTimeout)
		defer cancel()
	}

	text, err := u.agent.Eval(ctx, only)
	if err != nil {
		return "", fmt.Errorf("agent.Eval: %w", err)
	}
	slog.Info("eval finished", "user_id", userId, "only", only)
	return text, nil
}

// Reset забывает историю чата (в агенте).
func (u *Usecase) Reset(ctx context.Context, chatId, userId int64) error {
	if !u.Allowed(userId) {
		return errs.NotAuthorized
	}

	if err := u.agent.Reset(ctx, conversationId(chatId)); err != nil {
		return fmt.Errorf("agent.Reset: %w", err)
	}

	return nil
}

// conversationId — беседа чата Telegram в агенте.
func conversationId(chatId int64) string {
	return "tg:" + strconv.FormatInt(chatId, 10)
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
