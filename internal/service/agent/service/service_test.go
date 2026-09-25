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

	"github.com/mechta-market/pulse_bot/internal/errs"
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

func TestAsk(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/ask", r.URL.Path)
		assert.Equal(t, "Bearer k-bot", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"answer":"память в норме","result":null,"incomplete":"",
			"charts":[{"title":"Память","type":"line","png":"UE5H"}],"duration_ms":1}`))
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
