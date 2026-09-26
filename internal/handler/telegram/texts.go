package telegram

import (
	agentModel "github.com/mechta-market/pulse_bot/internal/service/agent/model"
)

// buttonReset — надпись кнопки сброса; её нажатие приходит обычным текстом
const buttonReset = "🔄 Начать заново"

// тексты ответов бота (HTML)
const (
	textWelcome = "Привет! Я отвечаю на вопросы об инфраструктуре: что с сервисом, что с кластером, " +
		"что поменялось перед падением. Факты беру из pulse — Kubernetes, Prometheus, Loki, Alertmanager, GitHub.\n\n" +
		"Спрашивайте обычным текстом, например: <i>что с caravan?</i>\n\n" +
		"Кнопка «" + buttonReset + "» или /reset — начать разговор заново. /muted — какие уведомления приглушены."

	textDenied = "Извините, доступ к боту ограничен. Ваш Telegram ID: <code>%d</code> — " +
		"передайте его администратору бота."

	textReset        = "Начали заново: прошлый разговор забыт."
	textBusy         = "Ещё разбираюсь с предыдущим вопросом — дождитесь ответа."
	textNotText      = "Я понимаю только текстовые сообщения."
	textEmptyAnswer  = "Модель не дала ответа. Попробуйте переформулировать вопрос."
	textError        = "Не удалось получить ответ: %s\n\nПопробуйте ещё раз чуть позже."
	textTimeout      = "Не успел разобраться за отведённое время. Попробуйте сузить вопрос."
	textShuttingDown = "Бот перезапускается — повторите вопрос через минуту."

	textMuted          = "🔕 Приглушено: %s (%s). Что приглушено и вернуть — /muted"
	textUnmuted        = "🔔 Приглушение снято (%s): уведомления снова приходят."
	textUnmutedShort   = "🔔 Снова присылаю"
	textNothingMuted   = "Ничего не приглушено.\n\nПриглушить — кнопками под уведомлением или словами: <i>не присылай про caravan до понедельника</i>."
	textNotifyOff      = "Уведомления в этом боте не настроены."
	textCallbackStale  = "Кнопка устарела"
	textCallbackDenied = "Вам нельзя менять уведомления этого чата"
	textCallbackFailed = "Не получилось — попробуйте позже"
	// textReplyContext — вопрос ответом на сообщение бота: что это было за сообщение
	textReplyContext = "Ответ на сообщение бота:\n«%s»\n\n%s"

	textEvalDenied  = "Команда /eval — только для админов бота."
	textEvalStarted = "Запустил прогон эталонных вопросов агента — это 5–10 минут, пришлю таблицу."
	textEvalBusy    = "Прогон уже идёт — дождитесь его таблицы."
	textEvalFailed  = "Прогон не удался: %s"
)

// пометки о досрочно законченном разборе
var incompleteNotes = map[string]string{
	agentModel.IncompleteToolCalls: "Разбор неполный: исчерпан лимит обращений к pulse на один вопрос.",
	agentModel.IncompleteTimeout:   "Разбор неполный: исчерпано время на ответ.",
	agentModel.IncompleteOutput:    "Ответ оборван: модель упёрлась в лимит длины ответа.",
}
