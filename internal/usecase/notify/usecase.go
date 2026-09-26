// Package notify — уведомления агента в чатах Telegram: какие чаты получают ленту
// (NOTIFY_CHAT_IDS), кто может приглушать, лента и подтверждение доставки, приглушения. Лента,
// приглушения и курсор чата хранит агент (беседа — constant.ConversationId).
package notify

import (
	"context"
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_bot/internal/constant"
	"github.com/mechta-market/pulse_bot/internal/errs"
	"github.com/mechta-market/pulse_bot/internal/infra/metrics"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
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
		Help: "Уведомления агента по исходу: sent, muted, failed.",
	}, []string{"outcome"})
}

type Config struct {
	// Chats — чаты, которые получают уведомления: там любой участник может приглушать
	Chats []int64
	// AllowedUsers — в личке управлять приглушениями могут только они
	AllowedUsers []int64
}

type Usecase struct {
	chats   []int64
	notify  map[int64]struct{}
	allowed map[int64]struct{}
	agent   AgentI
}

func New(cfg Config, agent AgentI) *Usecase {
	return &Usecase{
		chats:   lo.Uniq(cfg.Chats),
		notify:  lo.SliceToMap(cfg.Chats, func(id int64) (int64, struct{}) { return id, struct{}{} }),
		allowed: lo.SliceToMap(cfg.AllowedUsers, func(id int64) (int64, struct{}) { return id, struct{}{} }),
		agent:   agent,
	}
}

// Chats — чаты уведомлений.
func (u *Usecase) Chats() []int64 {
	return u.chats
}

// NotifyChat — чат получает уведомления.
func (u *Usecase) NotifyChat(chatId int64) bool {
	_, ok := u.notify[chatId]
	return ok
}

// CanManage — может ли пользователь управлять приглушениями чата: в чате уведомлений — любой
// участник, иначе — пользователь из белого списка.
func (u *Usecase) CanManage(chatId, userId int64) bool {
	_, allowed := u.allowed[userId]
	return u.NotifyChat(chatId) || allowed
}

// Pending — новые уведомления чата (и приглушённые — у них MutedBy); подтверждать — Ack.
func (u *Usecase) Pending(ctx context.Context, chatId int64) ([]*agentModel.Notification, error) {
	items, err := u.agent.Notifications(ctx, constant.ConversationId(chatId), batch)
	if err != nil {
		return nil, fmt.Errorf("agent.Notifications: %w", err)
	}
	return items, nil
}

// Ack — чат получил уведомления до lastId; sent и muted — для метрик.
func (u *Usecase) Ack(ctx context.Context, chatId, lastId int64, sent, muted, failed int) error {
	metricNotifications.WithLabelValues("sent").Add(float64(sent))
	metricNotifications.WithLabelValues("muted").Add(float64(muted))
	metricNotifications.WithLabelValues("failed").Add(float64(failed))
	if err := u.agent.Ack(ctx, constant.ConversationId(chatId), lastId); err != nil {
		return fmt.Errorf("agent.Ack: %w", err)
	}
	return nil
}

// Mute — приглушить в чате сервис уведомления на duration (1h, 1d; пусто — навсегда). Ошибки:
// errs.NotAuthorized — пользователю нельзя.
func (u *Usecase) Mute(ctx context.Context, chatId, userId int64, userName string, notificationId int64, duration string) (*agentModel.Mute, error) {
	if !u.CanManage(chatId, userId) {
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
func (u *Usecase) Unmute(ctx context.Context, chatId, userId, muteId int64) error {
	if !u.CanManage(chatId, userId) {
		return errs.NotAuthorized
	}
	if err := u.agent.Unmute(ctx, constant.ConversationId(chatId), muteId); err != nil {
		return fmt.Errorf("agent.Unmute: %w", err)
	}
	return nil
}

// Mutes — приглушения чата и последние скрытые уведомления. Ошибки: errs.NotAuthorized.
func (u *Usecase) Mutes(ctx context.Context, chatId, userId int64) ([]*agentModel.Mute, []*agentModel.Notification, error) {
	if !u.CanManage(chatId, userId) {
		return nil, nil, errs.NotAuthorized
	}
	mutes, muted, err := u.agent.Mutes(ctx, constant.ConversationId(chatId), mutedShown)
	if err != nil {
		return nil, nil, fmt.Errorf("agent.Mutes: %w", err)
	}
	return mutes, muted, nil
}
