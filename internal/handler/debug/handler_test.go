package debug

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_bot/internal/errs"
	"github.com/mechta-market/pulse_bot/internal/handler/debug/dto"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
	chatModel "github.com/mechta-market/pulse_bot/internal/usecase/chat/model"
)

type fakeChat struct {
	questions []*chatModel.Question
	resets    []int64
	answer    *chatModel.Answer
	err       error
}

func (f *fakeChat) Ask(_ context.Context, q *chatModel.Question) (*chatModel.Answer, error) {
	f.questions = append(f.questions, q)
	return f.answer, f.err
}

func (f *fakeChat) Reset(_ context.Context, chatId, _ int64) error {
	f.resets = append(f.resets, chatId)
	return nil
}

func do(h http.Handler, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAsk(t *testing.T) {
	chat := &fakeChat{answer: &chatModel.Answer{
		Text:      "caravan в порядке",
		Steps:     2,
		ToolCalls: 1,
		Trace: []agentModel.ToolTrace{{
			Step:      1,
			Name:      "resolve_service",
			Arguments: `{"query":"caravan"}`,
			Status:    agentModel.ToolStatusOk,
			Output:    "абвгд",
			Duration:  1500 * time.Millisecond,
		}},
	}}
	h := New(chat, "secret")

	w := do(h, "secret", `{"chat_id":7,"text":"что с caravan?","output_limit":5}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	rep := &dto.AskRep{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), rep))
	assert.Equal(t, "caravan в порядке", rep.Answer)
	assert.Equal(t, 2, rep.Steps)
	require.Len(t, rep.Trace, 1)
	assert.Equal(t, "resolve_service", rep.Trace[0].Tool)
	assert.Equal(t, int64(1500), rep.Trace[0].DurationMs)
	// 5 байт — «аб» и половина «в»: половину отбрасываем
	assert.Equal(t, "аб", rep.Trace[0].Output)
	assert.True(t, rep.Trace[0].Truncated)
	assert.Equal(t, 10, rep.Trace[0].OutputBytes)

	require.Len(t, chat.questions, 1)
	assert.Equal(t, &chatModel.Question{ChatId: 7, Text: "что с caravan?"}, chat.questions[0])
}

func TestResetOnly(t *testing.T) {
	chat := &fakeChat{}

	w := do(New(chat, "secret"), "secret", `{"chat_id":7,"reset":true}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"answer":"","reset":true,"duration_ms":0,"steps":0,"tool_calls":0,
		"usage":{"input_tokens":0,"cached_tokens":0,"output_tokens":0,"reasoning_tokens":0},"trace":[]}`, w.Body.String())
	assert.Equal(t, []int64{7}, chat.resets)
	assert.Empty(t, chat.questions)
}

func TestErrors(t *testing.T) {
	h := New(&fakeChat{err: errs.Busy}, "secret")

	assert.Equal(t, http.StatusUnauthorized, do(h, "", `{}`).Code)
	assert.Equal(t, http.StatusUnauthorized, do(h, "wrong", `{}`).Code)
	assert.Equal(t, http.StatusBadRequest, do(h, "secret", `not json`).Code)
	assert.Equal(t, http.StatusConflict, do(h, "secret", `{"text":"q"}`).Code)

	r := httptest.NewRequest(http.MethodGet, Path, nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
