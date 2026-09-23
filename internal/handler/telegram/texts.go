package telegram

import (
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

// тексты ответов бота (HTML)
const (
	textWelcome = "Привет! Я отвечаю на вопросы об инфраструктуре: что с сервисом, что с кластером, " +
		"что поменялось перед падением. Факты беру из pulse — Kubernetes, Prometheus, Loki, Alertmanager, GitHub.\n\n" +
		"Спрашивайте обычным текстом, например: <i>что с caravan?</i>\n\n" +
		"/reset — начать разговор заново."

	textDenied = "Извините, доступ к боту ограничен. Ваш Telegram ID: <code>%d</code> — " +
		"передайте его администратору бота."

	textReset        = "Начали заново: прошлый разговор забыт."
	textBusy         = "Ещё разбираюсь с предыдущим вопросом — дождитесь ответа."
	textNotText      = "Я понимаю только текстовые сообщения."
	textEmptyAnswer  = "Модель не дала ответа. Попробуйте переформулировать вопрос."
	textError        = "Не удалось получить ответ: %s\n\nПопробуйте ещё раз чуть позже."
	textTimeout      = "Не успел разобраться за отведённое время. Попробуйте сузить вопрос."
	textShuttingDown = "Бот перезапускается — повторите вопрос через минуту."
)

// пометки о досрочно законченном разборе
var incompleteNotes = map[string]string{
	agentModel.IncompleteToolCalls: "Разбор неполный: исчерпан лимит обращений к pulse на один вопрос.",
	agentModel.IncompleteTimeout:   "Разбор неполный: исчерпано время на ответ.",
	agentModel.IncompleteOutput:    "Ответ оборван: модель упёрлась в лимит длины ответа.",
}
