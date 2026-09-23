package model

// Question — вопрос пользователя в чате.
type Question struct {
	ChatId int64
	UserId int64
	Text   string
}

// Answer — ответ бота. Incomplete — почему разбор закончен досрочно
// (agent/model.Incomplete*); пусто — ответ полный.
type Answer struct {
	Text       string
	Incomplete string
}
