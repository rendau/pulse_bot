package model

import (
	"encoding/json"
	"time"
)

// причины, по которым агент закончил разбор досрочно (Answer.Incomplete)
const (
	IncompleteToolCalls = "tool_calls"
	IncompleteTimeout   = "timeout"
	IncompleteOutput    = "output"
)

// AskReq — вопрос агенту из чата.
type AskReq struct {
	ConversationId string // беседа: у бота — чат Telegram
	UserId         string
	UserName       string
	Question       string
}

// Answer — ответ агента.
type Answer struct {
	Text string
	// Incomplete — разбор закончен досрочно: timeout | tool_calls | output; пусто — полный
	Incomplete string
	Charts     []Chart
	// HumanReplies — ответы ручек сервисов только для человека: показать как есть, модель их не видела
	HumanReplies []HumanReply
}

// HumanReply — ответ ручки сервиса для человека (audience: human): Data — как ответил сервис.
type HumanReply struct {
	Service    string
	EndpointId string
	Title      string
	Params     map[string]any
	StatusCode int
	RequestId  string
	Data       json.RawMessage
	Truncated  bool
	// MaskedFields — сколько значений полей с именем секрета pulse заменил маской
	MaskedFields int
}

// Chart — картинка графика к ответу.
type Chart struct {
	Title string
	Png   []byte
}

// Notification — уведомление наблюдателя агента для беседы. MutedBy — приглушение беседы, под
// которое оно попало: не показывать, но подтвердить.
type Notification struct {
	Id           int64
	At           time.Time
	Kind         string // alert | deploy | logs | self | public | security (атака: Service пуст, Key — адрес)
	Service      string
	Key          string
	Severity     string // critical | warning | info
	Title        string
	Text         string // Markdown как у ответов (формат telegram)
	Investigated bool
	MutedBy      *int64
	// NotSubscribed — у беседы есть подписки, и уведомление ни под одну не подходит
	NotSubscribed bool
}

// Subscription — подписка беседы: что присылать (пустое поле — любое; нет подписок — всё).
type Subscription struct {
	Id          int64
	Service     string
	Kind        string
	MinSeverity string // info | warning | critical
	Note        string
	CreatedBy   string
}

// Mute — приглушение уведомлений в беседе; пустые поля — любое, Until nil — навсегда.
type Mute struct {
	Id         int64
	Service    string
	Kind       string
	Key        string
	Until      *time.Time
	Note       string
	CreatedBy  string
	Suppressed int // сколько уведомлений уже скрыло
}

// MuteReq — приглушить в беседе сервис уведомления NotificationId на Duration (30m, 1d; пусто — навсегда).
type MuteReq struct {
	ConversationId string
	NotificationId int64
	Duration       string
	UserId         string
	UserName       string
}

// решение по адресу атаки (BlockReq.Action, Block.Action)
const (
	BlockActionBlock = "block"
	BlockActionSkip  = "skip"
)

// BlockReq — решение админа по адресу атаки уведомления NotificationId (вид security): заблокировать
// в ruto на Duration (24h, 168h) или не блокировать (skip, без Duration). Блокирует агент.
type BlockReq struct {
	ConversationId string
	NotificationId int64
	Action         string
	Duration       string
	UserId         string
	UserName       string
}

// Block — что стало с адресом: Refused непуст — ruto отказал, адрес не заблокирован; Repeat — решение
// уже было принято раньше (By, At, Until — того решения); Until nil — skip или блок без срока.
type Block struct {
	NotificationId int64
	Ip             string
	Action         string
	Until          *time.Time
	ByName         string
	At             time.Time
	Repeat         bool
	Refused        string
}
