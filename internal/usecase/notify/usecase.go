// Package notify — уведомления агента в чатах Telegram: лента разрешённых чатов (что приходит —
// по подпискам чата; без них — ничего), подтверждение доставки, подписки и приглушения. Лента,
// настройки и курсор чата хранит агент (беседа — constant.ConversationId).
package notify

import (
	"context"
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	"github.com/rendau/pulse_bot/internal/constant"
	"github.com/rendau/pulse_bot/internal/errs"
	"github.com/rendau/pulse_bot/internal/infra/metrics"
	agentModel "github.com/rendau/pulse_bot/internal/service/agent/model"
	"github.com/rendau/pulse_bot/internal/usecase/notify/model"
)

const (
	// batch — уведомлений за один опрос чата
	batch = 20
	// mutedShown — сколько последних скрытых уведомлений показывать в /muted
	mutedShown = 5
)

var metricNotifications *prometheus.CounterVec

func init() {
	metricNotifications = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "notification_total",
		Help: "Уведомления агента по исходу: sent, muted, not_subscribed, failed.",
	}, []string{"outcome"})
}

type Config struct {
	// AllowedChats — разрешённые чаты: читают ленту и настраивают уведомления (в группе — любой участник)
	AllowedChats []int64
}

type Usecase struct {
	chats   []int64
	allowed map[int64]struct{}
	agent   AgentI
}

func New(cfg Config, agent AgentI) *Usecase {
	return &Usecase{
		chats:   lo.Uniq(cfg.AllowedChats),
		allowed: lo.SliceToMap(cfg.AllowedChats, func(id int64) (int64, struct{}) { return id, struct{}{} }),
		agent:   agent,
	}
}

// Chats — чаты, чью ленту забирать (разрешённые; что им приходит — по их подпискам).
func (u *Usecase) Chats() []int64 {
	return u.chats
}

// CanManage — можно ли в чате менять настройки уведомлений: разрешённый чат, любой участник.
func (u *Usecase) CanManage(chatId int64) bool {
	_, ok := u.allowed[chatId]
	return ok
}

// Pending — новые уведомления чата (и приглушённые — у них MutedBy); подтверждать — Ack.
func (u *Usecase) Pending(ctx context.Context, chatId int64) ([]*agentModel.Notification, error) {
	items, err := u.agent.Notifications(ctx, constant.ConversationId(chatId), batch)
	if err != nil {
		return nil, fmt.Errorf("agent.Notifications: %w", err)
	}
	return items, nil
}

// Ack — чат получил уведомления до lastId; delivery — что с ними стало (метрики).
func (u *Usecase) Ack(ctx context.Context, chatId, lastId int64, delivery model.Delivery) error {
	metricNotifications.WithLabelValues("sent").Add(float64(delivery.Sent))
	metricNotifications.WithLabelValues("muted").Add(float64(delivery.Muted))
	metricNotifications.WithLabelValues("not_subscribed").Add(float64(delivery.NotSubscribed))
	metricNotifications.WithLabelValues("failed").Add(float64(delivery.Failed))
	if err := u.agent.Ack(ctx, constant.ConversationId(chatId), lastId); err != nil {
		return fmt.Errorf("agent.Ack: %w", err)
	}
	return nil
}

// Mute — приглушить в чате сервис уведомления на duration (1h, 1d; пусто — навсегда). Ошибки:
// errs.NotAuthorized — пользователю нельзя.
func (u *Usecase) Mute(ctx context.Context, chatId, userId int64, userName string, notificationId int64, duration string) (*agentModel.Mute, error) {
	if !u.CanManage(chatId) {
		return nil, errs.NotAuthorized
	}
	m, err := u.agent.Mute(ctx, &agentModel.MuteReq{
		ConversationId: constant.ConversationId(chatId), NotificationId: notificationId, Duration: duration,
		UserId: strconv.FormatInt(userId, 10), UserName: userName,
	})
	if err != nil {
		return nil, fmt.Errorf("agent.Mute: %w", err)
	}
	return m, nil
}

// Unmute — снять приглушение чата. Ошибки: errs.NotAuthorized, errs.ObjectNotFound.
func (u *Usecase) Unmute(ctx context.Context, chatId, muteId int64) error {
	if !u.CanManage(chatId) {
		return errs.NotAuthorized
	}
	if err := u.agent.Unmute(ctx, constant.ConversationId(chatId), muteId); err != nil {
		return fmt.Errorf("agent.Unmute: %w", err)
	}
	return nil
}

// Mutes — приглушения чата и последние скрытые уведомления. Ошибки: errs.NotAuthorized.
func (u *Usecase) Mutes(ctx context.Context, chatId int64) ([]*agentModel.Mute, []*agentModel.Notification, error) {
	if !u.CanManage(chatId) {
		return nil, nil, errs.NotAuthorized
	}
	mutes, muted, err := u.agent.Mutes(ctx, constant.ConversationId(chatId), mutedShown)
	if err != nil {
		return nil, nil, fmt.Errorf("agent.Mutes: %w", err)
	}
	return mutes, muted, nil
}

// Subscriptions — подписки чата (пусто — уведомления не приходят). Ошибки: errs.NotAuthorized.
func (u *Usecase) Subscriptions(ctx context.Context, chatId int64) ([]*agentModel.Subscription, error) {
	if !u.CanManage(chatId) {
		return nil, errs.NotAuthorized
	}
	subs, err := u.agent.Subscriptions(ctx, constant.ConversationId(chatId))
	if err != nil {
		return nil, fmt.Errorf("agent.Subscriptions: %w", err)
	}
	return subs, nil
}

// Unsubscribe — убрать подписку чата. Ошибки: errs.NotAuthorized, errs.ObjectNotFound.
func (u *Usecase) Unsubscribe(ctx context.Context, chatId, id int64) error {
	if !u.CanManage(chatId) {
		return errs.NotAuthorized
	}
	if err := u.agent.Unsubscribe(ctx, constant.ConversationId(chatId), id); err != nil {
		return fmt.Errorf("agent.Unsubscribe: %w", err)
	}
	return nil
}
