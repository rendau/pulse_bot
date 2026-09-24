package app

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/mechta-market/pulse_bot/internal/config"
	handlerDebugP "github.com/mechta-market/pulse_bot/internal/handler/debug"
)

// DebugHttpServerCreate строит отладочный HTTP-сервер с единственной ручкой
// POST /debug/ask. Разбор идёт до AGENT_TIMEOUT — таймаут записи с запасом поверх
// него; запросы живут в ctx приложения, чтобы остановка отменяла идущие разборы.
func DebugHttpServerCreate(port string, handler http.Handler, ctx context.Context) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(handlerDebugP.Path, handler)

	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      config.Conf.AgentTimeout + 30*time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    64 * 1024,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}
