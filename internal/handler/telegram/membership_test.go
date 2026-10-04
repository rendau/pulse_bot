package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGroup — группа не из списка разрешённых.
const newGroup = int64(-1002233445566)

func memberOf(status models.ChatMemberType) models.ChatMember {
	return models.ChatMember{Type: status}
}

func memberUpdate(chatId int64, chatType models.ChatType, from, to models.ChatMember) *models.ChatMemberUpdated {
	return &models.ChatMemberUpdated{
		Chat:          models.Chat{ID: chatId, Type: chatType, Title: "Дежурные <SRE>"},
		From:          models.User{ID: 7, FirstName: "Иван", LastName: "Петров", Username: "ivan"},
		OldChatMember: from,
		NewChatMember: to,
	}
}

func TestJoined(t *testing.T) {
	left, member := memberOf(models.ChatMemberTypeLeft), memberOf(models.ChatMemberTypeMember)

	sender := &fakeSender{}
	h := New(&fakeChat{admins: []int64{42, 43}}, nil, 1, "pulse_bot")
	h.joined(context.Background(), sender, memberUpdate(newGroup, models.ChatTypeSupergroup, left, member))

	require.Len(t, sender.sent, 2, "каждому админу — в личный чат")
	assert.Equal(t, int64(42), sender.sent[0].ChatID)
	assert.Equal(t, int64(43), sender.sent[1].ChatID)
	text := sender.sent[0].Text
	assert.Equal(t, models.ParseModeHTML, sender.sent[0].ParseMode)
	assert.Contains(t, text, "<b>Дежурные &lt;SRE&gt;</b>", "название экранировано")
	assert.Contains(t, text, "ID группы: <code>-1002233445566</code>")
	assert.Contains(t, text, "Кто добавил: Иван Петров (@ivan)")
	assert.Contains(t, text, textGroupNotAllowed)

	// группа уже в списке (бота убрали и добавили заново)
	sender = &fakeSender{}
	h.joined(context.Background(), sender, memberUpdate(group, models.ChatTypeSupergroup, left, member))
	require.Len(t, sender.sent, 2)
	assert.Contains(t, sender.sent[0].Text, "<code>-100500</code>")
	assert.Contains(t, sender.sent[0].Text, textGroupAllowed)
}

func TestJoined_OnlyWhenAddedToGroup(t *testing.T) {
	left, member := memberOf(models.ChatMemberTypeLeft), memberOf(models.ChatMemberTypeMember)
	admin, banned := memberOf(models.ChatMemberTypeAdministrator), memberOf(models.ChatMemberTypeBanned)
	restricted := func(isMember bool) models.ChatMember {
		return models.ChatMember{Type: models.ChatMemberTypeRestricted, Restricted: &models.ChatMemberRestricted{IsMember: isMember}}
	}

	tests := []struct {
		name     string
		chatType models.ChatType
		from, to models.ChatMember
		want     bool
	}{
		{"добавили", models.ChatTypeGroup, left, member, true},
		{"добавили сразу админом", models.ChatTypeSupergroup, left, admin, true},
		{"вернули после блокировки", models.ChatTypeSupergroup, banned, member, true},
		{"добавили с ограничениями", models.ChatTypeSupergroup, left, restricted(true), true},
		{"сделали админом — уже был в группе", models.ChatTypeSupergroup, member, admin, false},
		{"убрали из группы", models.ChatTypeSupergroup, member, left, false},
		{"заблокировали", models.ChatTypeSupergroup, admin, banned, false},
		{"ограничили и убрали", models.ChatTypeSupergroup, member, restricted(false), false},
		{"личный чат: бота разблокировали", models.ChatTypePrivate, banned, member, false},
		{"канал", models.ChatTypeChannel, left, admin, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			h := New(&fakeChat{admins: []int64{42}}, nil, 1, "pulse_bot")
			h.joined(context.Background(), sender, memberUpdate(newGroup, tt.chatType, tt.from, tt.to))

			assert.Equal(t, tt.want, len(sender.sent) == 1)
		})
	}
}

func TestMigrated(t *testing.T) {
	migration := func(oldId, newId int64) *models.Message {
		return &models.Message{
			ID:              5,
			Chat:            models.Chat{ID: oldId, Type: models.ChatTypeGroup, Title: "Дежурные"},
			MigrateToChatID: newId,
		}
	}

	tests := []struct {
		name         string
		oldId, newId int64
		want         string
	}{
		{"в списке прежний id", group, newGroup, textGroupStaleId},
		{"группы в списке не было", -777, newGroup, textGroupNotAllowed},
		{"новый id уже в списке", -777, group, textGroupAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender := &fakeSender{}
			h := New(&fakeChat{admins: []int64{42}}, nil, 1, "pulse_bot")
			h.migrated(context.Background(), sender, migration(tt.oldId, tt.newId))

			require.Len(t, sender.sent, 1)
			assert.Equal(t, int64(42), sender.sent[0].ChatID)
			text := sender.sent[0].Text
			assert.Contains(t, text, "<b>Дежурные</b> стала супергруппой")
			assert.Contains(t, text, "ID группы: <code>"+strconv.FormatInt(tt.newId, 10)+"</code>")
			assert.Contains(t, text, "Прежний: <code>"+strconv.FormatInt(tt.oldId, 10)+"</code>")
			assert.Contains(t, text, tt.want)
		})
	}
}

// telegramStub — Bot API: запоминает sendMessage (chat_id → текст).
type telegramStub struct {
	mu   sync.Mutex
	sent map[string]string
}

func (s *telegramStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/sendMessage") {
		_ = r.ParseMultipartForm(1 << 20)
		s.mu.Lock()
		s.sent[r.FormValue("chat_id")] = r.FormValue("text")
		s.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"}}}`))
}

// Обновления в том виде, как их присылает Telegram: разбор библиотекой, Handle, отправка админу.
func TestHandle_GroupUpdates(t *testing.T) {
	tests := []struct {
		name   string
		update string
		want   []string
	}{
		{
			"бота добавили в группу",
			`{"update_id": 100, "my_chat_member": {
				"chat": {"id": -1002233445566, "title": "Дежурные", "type": "supergroup"},
				"from": {"id": 7, "is_bot": false, "first_name": "Иван", "username": "ivan"},
				"date": 1790000000,
				"old_chat_member": {"user": {"id": 1, "is_bot": true, "first_name": "pulse", "username": "pulse_bot"}, "status": "left"},
				"new_chat_member": {"user": {"id": 1, "is_bot": true, "first_name": "pulse", "username": "pulse_bot"}, "status": "member"}
			}}`,
			[]string{"Бота добавили в группу <b>Дежурные</b>", "<code>-1002233445566</code>", "Иван (@ivan)", textGroupNotAllowed},
		},
		{
			"группа стала супергруппой",
			`{"update_id": 101, "message": {
				"message_id": 5,
				"from": {"id": 7, "is_bot": false, "first_name": "Иван"},
				"chat": {"id": -100500, "title": "Дежурные", "type": "group"},
				"date": 1790000000,
				"migrate_to_chat_id": -1002233445566
			}}`,
			[]string{"стала супергруппой", "ID группы: <code>-1002233445566</code>", "Прежний: <code>-100500</code>", textGroupStaleId},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &telegramStub{sent: map[string]string{}}
			srv := httptest.NewServer(stub)
			defer srv.Close()

			b, err := bot.New("token", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
			require.NoError(t, err)

			var update models.Update
			require.NoError(t, json.Unmarshal([]byte(tt.update), &update))

			h := New(&fakeChat{admins: []int64{42}}, nil, 1, "pulse_bot")
			h.Handle(context.Background(), b, &update)
			h.Wait()

			require.Len(t, stub.sent, 1)
			for _, part := range tt.want {
				assert.Contains(t, stub.sent["42"], part)
			}
		})
	}
}
