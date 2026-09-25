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
	domainDialogRepoMemP "github.com/mechta-market/pulse_bot/internal/domain/dialog/repo/mem"
	domainDialogServiceP "github.com/mechta-market/pulse_bot/internal/domain/dialog/service"
	handlerDebugP "github.com/mechta-market/pulse_bot/internal/handler/debug"
	handlerTelegramP "github.com/mechta-market/pulse_bot/internal/handler/telegram"
	"github.com/mechta-market/pulse_bot/internal/infra/httpx"
	serviceAgentServiceP "github.com/mechta-market/pulse_bot/internal/service/agent/service"
	serviceChartServiceP "github.com/mechta-market/pulse_bot/internal/service/chart/service"
	"github.com/mechta-market/pulse_bot/internal/service/llm"
	serviceLlmOpenaiServiceP "github.com/mechta-market/pulse_bot/internal/service/llm/openai/service"
	servicePulseServiceP "github.com/mechta-market/pulse_bot/internal/service/pulse/service"
	usecaseChatP "github.com/mechta-market/pulse_bot/internal/usecase/chat"
)

const (
	// telegramPollTimeout — long polling getUpdates; http-таймауты клиента Telegram —
	// с запасом поверх него
	telegramPollTimeout = 30 * time.Second
	telegramHttpTimeout = 45 * time.Second
)

type App struct {
	pulse *servicePulseServiceP.Service

	telegramBot     *bot.Bot
	telegramHandler *handlerTelegramP.Handler
	telegramWg      sync.WaitGroup

	debugHttpServer  *http.Server // nil — DEBUG_CHAT_TOKEN не задан
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

	// llm
	var llmProvider llm.Provider
	switch config.Conf.LlmProvider {
	case constant.LlmProviderOpenai:
		llmProvider = serviceLlmOpenaiServiceP.New(
			serviceLlmOpenaiServiceP.Config{
				ApiKey:          config.Conf.OpenaiApiKey,
				BaseUrl:         config.Conf.OpenaiBaseUrl,
				Model:           config.Conf.LlmModel,
				ReasoningEffort: config.Conf.LlmReasoningEffort,
				MaxOutputTokens: config.Conf.LlmMaxOutputTokens,
			},
			// ответ без стриминга приходит целиком после генерации (с reasoning —
			// минуты): заголовков ждём до общего таймаута разбора, его держит контекст
			httpx.New(httpx.Config{ResponseHeaderTimeout: config.Conf.AgentTimeout, VerifyTLS: true}),
		)
	default:
		errCheck(fmt.Errorf("unknown LLM_PROVIDER %q", config.Conf.LlmProvider), "llm")
	}
	slog.Info("llm", "provider", llmProvider.Name(), "model", config.Conf.LlmModel, "reasoning_effort", config.Conf.LlmReasoningEffort)

	// pulse (MCP)
	a.pulse = servicePulseServiceP.New(
		config.Conf.PulseMcpUrl,
		config.Conf.PulseMcpToken,
		// таймаут вызова инструмента держит контекст разбора
		httpx.New(httpx.Config{ResponseHeaderTimeout: 2 * time.Minute}),
	)

	// chart
	chartService := serviceChartServiceP.New(serviceChartServiceP.Config{})

	// agent
	agentService := serviceAgentServiceP.New(
		serviceAgentServiceP.Config{
			MaxToolCalls: config.Conf.AgentMaxToolCalls,
			Timeout:      config.Conf.AgentTimeout,
		},
		llmProvider, a.pulse, chartService,
	)

	// dialog
	dialogRepo := domainDialogRepoMemP.New()
	dialogService := domainDialogServiceP.New(
		domainDialogServiceP.Config{MaxTurns: config.Conf.HistoryMaxTurns, Ttl: config.Conf.HistoryTtl},
		dialogRepo,
	)

	// chat
	chatUsecase := usecaseChatP.New(
		usecaseChatP.Config{AllowedUsers: config.Conf.TelegramAllowedUsers},
		dialogService, agentService,
	)
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

	// debug http server (/debug/ask): свой usecase чата — без белого списка
	// (доступ по токену) и со своей историей, не пересекается с Telegram
	if config.Conf.DebugChatToken != "" {
		debugDialogService := domainDialogServiceP.New(
			domainDialogServiceP.Config{MaxTurns: config.Conf.HistoryMaxTurns, Ttl: config.Conf.HistoryTtl},
			domainDialogRepoMemP.New(),
		)
		debugChatUsecase := usecaseChatP.New(usecaseChatP.Config{AllowAll: true}, debugDialogService, agentService)
		debugHandler := handlerDebugP.New(debugChatUsecase, config.Conf.DebugChatToken)

		a.debugHttpServer = DebugHttpServerCreate(config.Conf.HttpPort, debugHandler, a.ctx)
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

	// debug http server
	if a.debugHttpServer != nil {
		go func() {
			err := a.debugHttpServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCheck(err, "debug-http-server stopped")
			}
		}()
		slog.Info("debug-http-server started " + a.debugHttpServer.Addr)
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

	// debug http server
	if a.debugHttpServer != nil {
		ctx, ctxCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ctxCancel()

		if err := a.debugHttpServer.Shutdown(ctx); err != nil {
			slog.Error("debug-http-server shutdown error", "error", err)
			a.exitCode = 1
		}
	}

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

	if err := a.pulse.Close(); err != nil {
		slog.Warn("pulse session close", "error", err)
	}

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
