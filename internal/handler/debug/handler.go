// Package debug — отладочная HTTP-ручка: вопрос боту в обход Telegram с ходом
// разбора в ответе. Поднимается только при заданном DEBUG_CHAT_TOKEN.
package debug

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mechta-market/pulse_bot/internal/errs"
	"github.com/mechta-market/pulse_bot/internal/handler/debug/dto"
	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

// Path — единственная ручка отладочного сервера.
const Path = "/debug/ask"

const maxBodyBytes = 64 * 1024

type Handler struct {
	chat  ChatUsecaseI
	token []byte
}

func New(chat ChatUsecaseI, token string) *Handler {
	return &Handler{chat: chat, token: []byte(token)}
}

// ServeHTTP — POST /debug/ask, bearer-токен DEBUG_CHAT_TOKEN.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(got), h.token) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="debug"`)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	req := &dto.AskReq{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	ctx := r.Context()
	started := time.Now()

	if req.Reset {
		if err := h.chat.Reset(ctx, req.ChatId, 0); err != nil {
			writeFail(w, r, req.ChatId, err)
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			writeJson(w, http.StatusOK, &dto.AskRep{Reset: true, Trace: []*dto.ToolTraceRep{}})
			return
		}
	}

	answer, err := h.chat.Ask(ctx, &chatModel.Question{ChatId: req.ChatId, Text: req.Text})
	if err != nil {
		writeFail(w, r, req.ChatId, err)
		return
	}

	rep := dto.EncodeAskRep(answer, req.OutputLimit)
	rep.Reset = req.Reset
	rep.DurationMs = time.Since(started).Milliseconds()

	writeJson(w, http.StatusOK, rep)
}

func writeFail(w http.ResponseWriter, r *http.Request, chatId int64, err error) {
	switch {
	case errors.Is(err, errs.InvalidRequest):
		writeError(w, http.StatusBadRequest, "empty text")
	case errors.Is(err, errs.Busy):
		writeError(w, http.StatusConflict, "previous question in this chat is still running")
	case r.Context().Err() != nil:
		// клиент ушёл или бот останавливается
		writeError(w, http.StatusServiceUnavailable, "canceled: "+err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, err.Error())
	default:
		slog.Error("debug question failed", "chat_id", chatId, "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJson(w, status, &dto.ErrorRep{Error: msg})
}

func writeJson(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("debug: write response", "error", err)
	}
}
