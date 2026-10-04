package telegram

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/samber/lo"
)

// joined — бота добавили в группу: админам в личные чаты — её id для TELEGRAM_ALLOWED_CHAT_IDS
// (и в лог — если админов нет). В саму группу бот не пишет: неразрешённым он не отвечает.
func (h *Handler) joined(ctx context.Context, sender SenderI, upd *models.ChatMemberUpdated) {
	chat := upd.Chat
	if !isGroup(chat.Type) || inChat(upd.OldChatMember) || !inChat(upd.NewChatMember) {
		return
	}

	allowed := h.chat.Allowed(chat.ID)
	slog.Info("telegram: bot added to group", "chat_id", chat.ID, "title", chat.Title, "allowed", allowed, "by", upd.From.ID)

	h.notifyAdmins(ctx, sender, fmt.Sprintf(textGroupJoined,
		html.EscapeString(groupTitle(chat)), chat.ID, html.EscapeString(userLabel(upd.From)),
		lo.Ternary(allowed, textGroupAllowed, textGroupNotAllowed)))
}

// migrated — группа стала супергруппой (служебное сообщение в прежней группе): её id сменился,
// прежний в списке разрешённых больше не работает — админам новый.
func (h *Handler) migrated(ctx context.Context, sender SenderI, msg *models.Message) {
	oldId, newId := msg.Chat.ID, msg.MigrateToChatID

	allowed := h.chat.Allowed(newId)
	slog.Info("telegram: group migrated to supergroup", "chat_id", newId, "old_chat_id", oldId, "title", msg.Chat.Title, "allowed", allowed)

	status := textGroupNotAllowed
	switch {
	case allowed:
		status = textGroupAllowed
	case h.chat.Allowed(oldId):
		status = textGroupStaleId
	}

	h.notifyAdmins(ctx, sender, fmt.Sprintf(textGroupMigrated, html.EscapeString(groupTitle(msg.Chat)), newId, oldId, status))
}

// notifyAdmins — сообщение (HTML) в личные чаты админов бота.
func (h *Handler) notifyAdmins(ctx context.Context, sender SenderI, text string) {
	for _, adminId := range h.chat.Admins() {
		h.sendText(ctx, sender, adminId, text)
	}
}

func isGroup(t models.ChatType) bool {
	return t == models.ChatTypeGroup || t == models.ChatTypeSupergroup
}

// inChat — участник в чате: в том числе ограниченный, но не вышедший и не заблокированный.
func inChat(m models.ChatMember) bool {
	switch m.Type {
	case models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator, models.ChatMemberTypeMember:
		return true
	case models.ChatMemberTypeRestricted:
		return m.Restricted != nil && m.Restricted.IsMember
	}
	return false
}

func groupTitle(chat models.Chat) string {
	return lo.CoalesceOrEmpty(strings.TrimSpace(chat.Title), "без названия")
}

// userLabel — «Иван Петров (@ivan)».
func userLabel(u models.User) string {
	name := lo.CoalesceOrEmpty(strings.TrimSpace(u.FirstName+" "+u.LastName), "кто-то")
	if u.Username != "" {
		name += " (@" + u.Username + ")"
	}
	return name
}
