package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse_bot/internal/config"
	"github.com/mechta-market/pulse_bot/internal/constant"
	handlerTelegramP "github.com/mechta-market/pulse_bot/internal/handler/telegram"
	"github.com/mechta-market/pulse_bot/internal/infra/httpx"
	serviceAgentServiceP "github.com/mechta-market/pulse_bot/internal/service/agent/service"
	usecaseChatP "github.com/mechta-market/pulse_bot/internal/usecase/chat"
	usecaseNotifyP "github.com/mechta-market/pulse_bot/internal/usecase/notify"
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
	notifier        *handlerTelegramP.Notifier

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
	// разрешённые чаты: из списка и личные чаты админов
	allowedChats := lo.Uniq(append(slices.Clone(config.Conf.TelegramAllowedChatIds), config.Conf.TelegramAdminUsers...))
	if len(allowedChats) == 0 {
		slog.Warn("TELEGRAM_ALLOWED_CHAT_IDS and TELEGRAM_ADMIN_USERS are empty: bot will deny everyone")
	}

	chatUsecase := usecaseChatP.New(usecaseChatP.Config{
		AllowedChats: allowedChats,
		AdminUsers:   config.Conf.TelegramAdminUsers,
		AskTimeout:   config.Conf.AgentTimeout,
		EvalTimeout:  config.Conf.AgentEvalTimeout,
	}, agentService)

	// notify (уведомления агента: разрешённые чаты, что приходит — по их подпискам)
	notifyUsecase := usecaseNotifyP.New(usecaseNotifyP.Config{AllowedChats: allowedChats}, agentService)

	// telegram
	{
		a.telegramBot, err = bot.New(config.Conf.TelegramBotToken,
			bot.WithHTTPClient(telegramPollTimeout, httpx.New(httpx.Config{
				Timeout:               telegramHttpTimeout,
				ResponseHeaderTimeout: telegramHttpTimeout,
				VerifyTLS:             true,
			})),
			bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "callback_query"}),
			// обработчику нужен id бота (ответы на его сообщения) — он создаётся после бота
			bot.WithDefaultHandler(func(ctx context.Context, b *bot.Bot, update *models.Update) {
				a.telegramHandler.Handle(ctx, b, update)
			}),
			bot.WithNotAsyncHandlers(),
			bot.WithErrorsHandler(func(err error) {
				if a.ctx.Err() == nil {
					slog.Error("telegram", "error", err)
				}
			}),
		)
		errCheck(err, "telegram bot init")

		// имя бота — для упоминаний в группах; не узнали — в группах отвечаем на команды и ответы
		username := ""
		meCtx, meCancel := context.WithTimeout(a.ctx, 10*time.Second)
		if me, meErr := a.telegramBot.GetMe(meCtx); meErr != nil {
			slog.Warn("telegram getMe: mentions in groups are off", "error", meErr)
		} else {
			username = me.Username
		}
		meCancel()

		a.telegramHandler = handlerTelegramP.New(chatUsecase, notifyUsecase, a.telegramBot.ID(), username)
		a.notifier = handlerTelegramP.NewNotifier(notifyUsecase, a.telegramBot, config.Conf.NotifyPollInterval)
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

	// notifier (лента уведомлений агента)
	a.notifier.Start(a.ctx)

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

	// notifier
	a.notifier.Wait()
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
