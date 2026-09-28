package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_bot/internal/errs"
	agentModel "github.com/rendau/pulse_bot/internal/service/agent/model"
)

func TestAsk(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/ask", r.URL.Path)
		assert.Equal(t, "Bearer k-bot", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"answer":"память в норме","result":null,"incomplete":"",
			"charts":[{"title":"Память","type":"line","png":"UE5H"}],"duration_ms":1,
			"human_replies":[{"service":"seller","endpoint_id":"order_raw","params":{"number":"123"},"status_code":200,
				"request_id":"pulse-1","data":{"number":"123"},"masked_fields":1}]}`))
	}))
	defer srv.Close()

	answer, err := New(srv.URL+"/", "k-bot", srv.Client()).Ask(context.Background(),
		&agentModel.AskReq{ConversationId: "tg:42", UserId: "7", UserName: "Даурен", Question: "память caravan?"})
	require.NoError(t, err)

	assert.Equal(t, "telegram", got["format"])
	assert.Equal(t, "png", got["charts"])
	assert.Equal(t, "tg:42", got["conversation_id"])
	assert.Equal(t, map[string]any{"id": "7", "name": "Даурен"}, got["user"])

	assert.Equal(t, "память в норме", answer.Text)
	require.Len(t, answer.Charts, 1)
	assert.Equal(t, []byte("PNG"), answer.Charts[0].Png)
	require.Len(t, answer.HumanReplies, 1)
	reply := answer.HumanReplies[0]
	assert.Equal(t, "order_raw", reply.EndpointId)
	assert.Equal(t, map[string]any{"number": "123"}, reply.Params)
	assert.JSONEq(t, `{"number":"123"}`, string(reply.Data))
	assert.Equal(t, 1, reply.MaskedFields)
}

func TestErrors(t *testing.T) {
	status, body := 0, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	svc := New(srv.URL, "k", srv.Client())

	status, body = http.StatusConflict, `{"code":"busy","error":"..."}`
	_, err := svc.Ask(context.Background(), &agentModel.AskReq{Question: "q"})
	require.ErrorIs(t, err, errs.Busy)

	status, body = http.StatusGatewayTimeout, `{"code":"timeout","error":"deadline"}`
	_, err = svc.Ask(context.Background(), &agentModel.AskReq{Question: "q"})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	status, body = http.StatusBadGateway, `bad gateway`
	err = svc.Reset(context.Background(), "tg:1")
	require.ErrorIs(t, err, errs.ServiceNA)
	assert.ErrorContains(t, err, "bad gateway")
	assert.False(t, errors.Is(err, errs.Busy))
}

func TestEval(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/eval", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"text":"Итого: 20/20","report":{}}`))
	}))
	defer srv.Close()

	text, err := New(srv.URL, "k", srv.Client()).Eval(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Equal(t, "Итого: 20/20", text)
	assert.Equal(t, []any{"a"}, got["only"])
}

func TestNotifyApi(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/notifications":
			_, _ = w.Write([]byte(`{"items":[{"id":42,"at":"2026-09-26T17:35:11+05:00","kind":"alert","service":"caravan",
				"key":"HighErrors","severity":"critical","title":"caravan: сбои","text":"- разбор","investigated":true,"muted_by":3}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mutes":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]any{"conversation_id": "tg:-100", "notification_id": float64(42), "duration": "1h",
				"user": map[string]any{"id": "7", "name": "Иван"}}, body)
			_, _ = w.Write([]byte(`{"id":7,"service":"caravan","kind":"","key":"","until":"2026-09-26T18:35:00+05:00","created_at":"2026-09-26T17:35:00+05:00"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","error":"object_not_found: mute 9"}`))
		default:
			_, _ = w.Write([]byte(`{"acked":true}`))
		}
	}))
	defer srv.Close()
	s := New(srv.URL, "k-bot", srv.Client())
	ctx := context.Background()

	items, err := s.Notifications(ctx, "tg:-100", 20)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(3), *items[0].MutedBy)
	assert.Equal(t, "17:35", items[0].At.Format("15:04"), "время — в поясе агента")

	require.NoError(t, s.Ack(ctx, "tg:-100", 42))

	m, err := s.Mute(ctx, &agentModel.MuteReq{ConversationId: "tg:-100", NotificationId: 42, Duration: "1h", UserId: "7", UserName: "Иван"})
	require.NoError(t, err)
	assert.Equal(t, "caravan", m.Service)
	require.NotNil(t, m.Until)

	err = s.Unmute(ctx, "tg:-100", 9)
	require.ErrorIs(t, err, errs.ObjectNotFound)

	assert.Equal(t, []string{
		"GET /v1/notifications?conversation_id=tg%3A-100&limit=20",
		"POST /v1/notifications/ack",
		"POST /v1/mutes",
		"DELETE /v1/mutes/9?conversation_id=tg%3A-100",
	}, calls)
}
