package model

// Delivery — что стало с уведомлениями одного опроса чата.
type Delivery struct {
	Sent          int
	Muted         int
	NotSubscribed int
	Failed        int
}
