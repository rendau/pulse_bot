package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-telegram/bot"

	"github.com/mechta-market/pulse_bot/internal/config"
	"github.com/mechta-market/pulse_bot/internal/constant"
	handlerTelegramP "github.com/mechta-market/pulse_bot/internal/handler/telegram"
	"github.com/mechta-market/pulse_bot/internal/infra/httpx"
	serviceAgentServiceP "github.com/mechta-market/pulse_bot/internal/service/agent/service"
	usecaseChatP "github.com/mechta-market/pulse_bot/internal/usecase/chat"
)

const (
	// telegramPollTimeout — long polling getUpdates; http-таймауты клиента Telegram —
	// с запасом поверх него
	telegramPollTimeout = 30 * time.Second
	telegramHttpTimeout = 45 * time.Second
)

type App struct {
	telegramBot     *bot.Bot
	telegramHandler *handlerTelegramP.Handler
	telegramWg      sync.WaitGroup

	systemHttpServer *http.Server

	ctx       context.Context
	ctxCancel context.CancelFunc

	exitCode int
}

func (a *App) Init() {
	var err error

	a.ctx, a.ctxCancel = context.WithCancel(context.Background())

	// logger
	initLogger(config.Conf.Debug, config.Conf.LogLevel)
	slog.Info("starting " + constant.ServiceName + " " + constant.Version)

	// agent (pulse_agent API)
	agentService := serviceAgentServiceP.New(
		config.Conf.AgentUrl,
		config.Conf.AgentKey,
		// ответ приходит целиком после разбора, прогон эталонов — после всех вопросов: заголовков
		// ждём до большего из таймаутов, конкретный вызов ограничивает контекст
		httpx.New(httpx.Config{ResponseHeaderTimeout: max(config.Conf.AgentTimeout, config.Conf.AgentEvalTimeout)}),
	)

	// chat
	chatUsecase := usecaseChatP.New(usecaseChatP.Config{
		AllowedUsers: config.Conf.TelegramAllowedUsers,
		AdminUsers:   config.Conf.TelegramAdminUsers,
		AskTimeout:   config.Conf.AgentTimeout,
		EvalTimeout:  config.Conf.AgentEvalTimeout,
	}, agentService)
	if len(config.Conf.TelegramAllowedUsers) == 0 {
		slog.Warn("TELEGRAM_ALLOWED_USERS is empty: bot will deny everyone")
	}

	// telegram
	{
		a.telegramHandler = handlerTelegramP.New(chatUsecase)

		a.telegramBot, err = bot.New(config.Conf.TelegramBotToken,
			bot.WithHTTPClient(telegramPollTimeout, httpx.New(httpx.Config{
				Timeout:               telegramHttpTimeout,
				ResponseHeaderTimeout: telegramHttpTimeout,
				VerifyTLS:             true,
			})),
			bot.WithAllowedUpdates(bot.AllowedUpdates{"message"}),
			bot.WithDefaultHandler(a.telegramHandler.Handle),
			bot.WithNotAsyncHandlers(),
			bot.WithErrorsHandler(func(err error) {
				if a.ctx.Err() == nil {
					slog.Error("telegram", "error", err)
				}
			}),
		)
		errCheck(err, "telegram bot init")
	}

	// system http server (healthcheck, docs, metrics)
	{
		a.systemHttpServer = SystemHttpServerCreate(config.Conf.SystemHttpPort)
	}
}

func (a *App) PreStartHook() {
	slog.Info("PreStartHook")
}

func (a *App) Start() {
	slog.Info("Starting")

	// telegram (long polling)
	{
		a.telegramWg.Go(func() {
			a.telegramBot.Start(a.ctx)
		})
		slog.Info("telegram bot started")
	}

	// system http server
	{
		go func() {
			err := a.systemHttpServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCheck(err, "system-http-server stopped")
			}
		}()
		slog.Info("system-http-server started " + a.systemHttpServer.Addr)
	}
}

func (a *App) Listen() {
	signalCtx, signalCtxCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer signalCtxCancel()

	// wait signal
	<-signalCtx.Done()
}

func (a *App) Stop() {
	slog.Info("Shutting down...")

	// stop context: останавливает long polling и отменяет идущие разборы
	a.ctxCancel()

	// system http server
	{
		ctx, ctxCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ctxCancel()

		if err := a.systemHttpServer.Shutdown(ctx); err != nil {
			slog.Error("system-http-server shutdown error", "error", err)
			a.exitCode = 1
		}
	}
}

func (a *App) WaitJobs() {
	slog.Info("waiting jobs")

	// telegram: сначала опрос обновлений, затем обработка принятых сообщений
	a.telegramWg.Wait()
	a.telegramHandler.Wait()
}

func (a *App) Exit() {
	slog.Info("Exit")

	os.Exit(a.exitCode)
}

func errCheck(err error, msg string) {
	if err != nil {
		if msg != "" {
			err = fmt.Errorf("%s: %w", msg, err)
		}
		slog.Error(err.Error())
		os.Exit(1)
	}
}
