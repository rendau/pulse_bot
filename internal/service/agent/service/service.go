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
	"strings"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse_bot/internal/errs"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

const (
	askPath   = "/v1/ask"
	resetPath = "/v1/reset"

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
	err := s.sendRequest(ctx, askPath, &askReq{
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

func (s *Service) Reset(ctx context.Context, conversationId string) error {
	return s.sendRequest(ctx, resetPath, &resetReq{ConversationId: conversationId}, &resetRep{})
}

// sendRequest — единственная точка отправки: JSON, ключ, коды ошибок API → errs.
func (s *Service) sendRequest(ctx context.Context, path string, reqObj, repObj any) error {
	body, err := json.Marshal(reqObj)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseUrl+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
			return errs.Busy
		case "invalid_request":
			return fmt.Errorf("%w: %s", errs.InvalidRequest, msg)
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
