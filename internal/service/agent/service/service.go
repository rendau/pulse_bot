// Package service — HTTP-клиент API pulse_agent. Бот — одна из систем-клиентов: свой ключ,
// формат telegram (Markdown для Telegram), графики только картинкой.
package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse_bot/internal/errs"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

const (
	askPath           = "/v1/ask"
	resetPath         = "/v1/reset"
	evalPath          = "/v1/eval"
	notificationsPath = "/v1/notifications"
	ackPath           = "/v1/notifications/ack"
	mutesPath         = "/v1/mutes"
	subscriptionsPath = "/v1/subscriptions"

	maxBodyBytes = 16 << 20 // ответ с тремя графиками в base64 — сотни КБ
)

type Service struct {
	baseUrl    string
	key        string
	httpClient *http.Client
}

// New — baseUrl — адрес агента (http://pulse-agent.default), key — ключ бота в API_KEYS агента;
// httpClient — из internal/infra/httpx (ответ приходит целиком после разбора: до минут).
func New(baseUrl, key string, httpClient *http.Client) *Service {
	return &Service{baseUrl: strings.TrimRight(baseUrl, "/"), key: key, httpClient: httpClient}
}

func (s *Service) Ask(ctx context.Context, req *agentModel.AskReq) (*agentModel.Answer, error) {
	rep := &askRep{}
	err := s.sendRequest(ctx, http.MethodPost, askPath, &askReq{
		Question:       req.Question,
		ConversationId: req.ConversationId,
		User:           userReq{Id: req.UserId, Name: req.UserName},
		Format:         "telegram",
		Charts:         "png",
	}, rep)
	if err != nil {
		return nil, err
	}

	answer := &agentModel.Answer{Text: rep.Answer, Incomplete: rep.Incomplete}
	for _, c := range rep.Charts {
		png, err := base64.StdEncoding.DecodeString(c.Png)
		if err != nil {
			return nil, fmt.Errorf("decode chart %q: %w", c.Title, err)
		}
		answer.Charts = append(answer.Charts, agentModel.Chart{Title: c.Title, Png: png})
	}
	return answer, nil
}

// Eval — прогон эталонных вопросов у агента: минуты, ответ — таблица.
func (s *Service) Eval(ctx context.Context, only []string) (string, error) {
	rep := &evalRep{}
	if err := s.sendRequest(ctx, http.MethodPost, evalPath, &evalReq{Only: only}, rep); err != nil {
		return "", err
	}
	return rep.Text, nil
}

func (s *Service) Reset(ctx context.Context, conversationId string) error {
	return s.sendRequest(ctx, http.MethodPost, resetPath, &resetReq{ConversationId: conversationId}, &resetRep{})
}

func (s *Service) Notifications(ctx context.Context, conversationId string, limit int) ([]*agentModel.Notification, error) {
	rep := &notificationsRep{}
	q := url.Values{"conversation_id": {conversationId}, "limit": {strconv.Itoa(limit)}}
	if err := s.sendRequest(ctx, http.MethodGet, notificationsPath+"?"+q.Encode(), nil, rep); err != nil {
		return nil, err
	}
	return lo.Map(rep.Items, decodeNotification), nil
}

func (s *Service) Ack(ctx context.Context, conversationId string, lastId int64) error {
	return s.sendRequest(ctx, http.MethodPost, ackPath, &ackReq{ConversationId: conversationId, LastId: lastId}, &struct{}{})
}

func (s *Service) Mute(ctx context.Context, req *agentModel.MuteReq) (*agentModel.Mute, error) {
	rep := &muteRep{}
	err := s.sendRequest(ctx, http.MethodPost, mutesPath, &muteReq{
		ConversationId: req.ConversationId, NotificationId: req.NotificationId, Duration: req.Duration,
		User: userReq{Id: req.UserId, Name: req.UserName},
	}, rep)
	if err != nil {
		return nil, err
	}
	return decodeMute(*rep, 0), nil
}

func (s *Service) Unmute(ctx context.Context, conversationId string, id int64) error {
	q := url.Values{"conversation_id": {conversationId}}
	return s.sendRequest(ctx, http.MethodDelete, mutesPath+"/"+strconv.FormatInt(id, 10)+"?"+q.Encode(), nil, &struct{}{})
}

func (s *Service) Mutes(ctx context.Context, conversationId string, mutedLimit int) ([]*agentModel.Mute, []*agentModel.Notification, error) {
	rep := &mutesRep{}
	q := url.Values{"conversation_id": {conversationId}, "muted_limit": {strconv.Itoa(mutedLimit)}}
	if err := s.sendRequest(ctx, http.MethodGet, mutesPath+"?"+q.Encode(), nil, rep); err != nil {
		return nil, nil, err
	}
	return lo.Map(rep.Mutes, decodeMute), lo.Map(rep.Muted, decodeNotification), nil
}

func (s *Service) Subscriptions(ctx context.Context, conversationId string) ([]*agentModel.Subscription, error) {
	rep := &subscriptionsRep{}
	q := url.Values{"conversation_id": {conversationId}}
	if err := s.sendRequest(ctx, http.MethodGet, subscriptionsPath+"?"+q.Encode(), nil, rep); err != nil {
		return nil, err
	}
	return lo.Map(rep.Subscriptions, func(v subscriptionRep, _ int) *agentModel.Subscription {
		return &agentModel.Subscription{Id: v.Id, Service: v.Service, Kind: v.Kind, MinSeverity: v.MinSeverity, Note: v.Note, CreatedBy: v.CreatedBy}
	}), nil
}

func (s *Service) Unsubscribe(ctx context.Context, conversationId string, id int64) error {
	q := url.Values{"conversation_id": {conversationId}}
	return s.sendRequest(ctx, http.MethodDelete, subscriptionsPath+"/"+strconv.FormatInt(id, 10)+"?"+q.Encode(), nil, &struct{}{})
}

// sendRequest — единственная точка отправки: JSON, ключ, коды ошибок API → errs.
func (s *Service) sendRequest(ctx context.Context, method, path string, reqObj, repObj any) error {
	var body io.Reader
	if reqObj != nil {
		raw, err := json.Marshal(reqObj)
		if err != nil {
			return fmt.Errorf("encode %s: %w", path, err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseUrl+path, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if reqObj != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+s.key)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: agent %s: %w", errs.ServiceNA, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		var e errorRep
		_ = json.Unmarshal(raw, &e)
		msg := lo.CoalesceOrEmpty(e.Error, strings.TrimSpace(string(raw)))
		switch e.Code {
		case "busy":
			return fmt.Errorf("%w: %s", errs.Busy, msg)
		case "forbidden":
			return fmt.Errorf("%w: %s", errs.NotAuthorized, msg)
		case "invalid_request":
			return fmt.Errorf("%w: %s", errs.InvalidRequest, msg)
		case "not_found":
			return fmt.Errorf("%w: %s", errs.ObjectNotFound, msg)
		case "timeout":
			return fmt.Errorf("%w: agent: %s", context.DeadlineExceeded, msg)
		default:
			return fmt.Errorf("%w: agent %s: status %d: %s", errs.ServiceNA, path, resp.StatusCode, msg)
		}
	}

	if err = json.Unmarshal(raw, repObj); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// transport models — подмножество контракта docs/agent-api.md, которое нужно боту

type askReq struct {
	Question       string  `json:"question"`
	ConversationId string  `json:"conversation_id,omitempty"`
	User           userReq `json:"user"`
	Format         string  `json:"format"`
	Charts         string  `json:"charts"`
}

type userReq struct {
	Id   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type askRep struct {
	Answer     string `json:"answer"`
	Incomplete string `json:"incomplete"`
	Charts     []struct {
		Title string `json:"title"`
		Png   string `json:"png"`
	} `json:"charts"`
}

type evalReq struct {
	Only []string `json:"only,omitempty"`
}

type evalRep struct {
	Text string `json:"text"`
}

type resetReq struct {
	ConversationId string `json:"conversation_id"`
}

type resetRep struct {
	Reset bool `json:"reset"`
}

type errorRep struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

type notificationRep struct {
	Id            int64     `json:"id"`
	At            time.Time `json:"at"`
	Kind          string    `json:"kind"`
	Service       string    `json:"service"`
	Key           string    `json:"key"`
	Severity      string    `json:"severity"`
	Title         string    `json:"title"`
	Text          string    `json:"text"`
	Investigated  bool      `json:"investigated"`
	MutedBy       *int64    `json:"muted_by"`
	NotSubscribed bool      `json:"not_subscribed"`
}

type subscriptionRep struct {
	Id          int64  `json:"id"`
	Service     string `json:"service"`
	Kind        string `json:"kind"`
	MinSeverity string `json:"min_severity"`
	Note        string `json:"note"`
	CreatedBy   string `json:"created_by"`
}

type subscriptionsRep struct {
	Subscriptions []subscriptionRep `json:"subscriptions"`
}

type notificationsRep struct {
	Items []notificationRep `json:"items"`
}

type ackReq struct {
	ConversationId string `json:"conversation_id"`
	LastId         int64  `json:"last_id"`
}

type muteReq struct {
	ConversationId string  `json:"conversation_id"`
	NotificationId int64   `json:"notification_id,omitempty"`
	Duration       string  `json:"duration,omitempty"`
	User           userReq `json:"user"`
}

type muteRep struct {
	Id         int64      `json:"id"`
	Service    string     `json:"service"`
	Kind       string     `json:"kind"`
	Key        string     `json:"key"`
	Until      *time.Time `json:"until"`
	Note       string     `json:"note"`
	CreatedBy  string     `json:"created_by"`
	Suppressed int        `json:"suppressed"`
}

type mutesRep struct {
	Mutes []muteRep         `json:"mutes"`
	Muted []notificationRep `json:"muted"`
}

func decodeNotification(v notificationRep, _ int) *agentModel.Notification {
	return &agentModel.Notification{
		Id: v.Id, At: v.At, Kind: v.Kind, Service: v.Service, Key: v.Key, Severity: v.Severity,
		Title: v.Title, Text: v.Text, Investigated: v.Investigated, MutedBy: v.MutedBy, NotSubscribed: v.NotSubscribed,
	}
}

func decodeMute(v muteRep, _ int) *agentModel.Mute {
	return &agentModel.Mute{
		Id: v.Id, Service: v.Service, Kind: v.Kind, Key: v.Key, Until: v.Until, Note: v.Note,
		CreatedBy: v.CreatedBy, Suppressed: v.Suppressed,
	}
}
