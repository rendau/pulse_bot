package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"slices"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/samber/lo"

	agentModel "github.com/rendau/pulse_bot/internal/service/agent/model"
)

// humanInlineLimit — ответ ручки для человека длиннее (JSON с отступами) уходит файлом.
const humanInlineLimit = 3500

// sendHumanReply — ответ ручки для человека как есть: заголовок и JSON блоком кода, большой —
// файлом <сервис>-<ручка>.json с заголовком в подписи.
func (h *Handler) sendHumanReply(ctx context.Context, sender SenderI, chatId int64, reply agentModel.HumanReply) {
	body := prettyJson(reply.Data)
	head := humanReplyHead(reply, len(body) > humanInlineLimit)

	if len(body) <= humanInlineLimit {
		_, err := sender.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatId,
			Text:      head + "\n<pre><code class=\"language-json\">" + html.EscapeString(body) + "</code></pre>",
			ParseMode: models.ParseModeHTML,
		})
		if err != nil {
			slog.Error("telegram: send human reply", "chat_id", chatId, "service", reply.Service, "endpoint", reply.EndpointId, "error", err)
		}
		return
	}

	_, err := sender.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:    chatId,
		Document:  &models.InputFileUpload{Filename: reply.Service + "-" + reply.EndpointId + ".json", Data: bytes.NewReader([]byte(body))},
		Caption:   truncate(head, captionLimit),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		slog.Error("telegram: send human reply file", "chat_id", chatId, "service", reply.Service, "endpoint", reply.EndpointId, "error", err)
	}
}

// humanReplyHead — заголовок: сервис · ручка, параметры, пометки (статус не 200, скрытые поля,
// урезано, файл).
func humanReplyHead(reply agentModel.HumanReply, inFile bool) string {
	lines := []string{fmt.Sprintf(textHumanReplyHead, html.EscapeString(reply.Service), html.EscapeString(reply.EndpointId))}
	if len(reply.Params) > 0 {
		names := lo.Keys(reply.Params)
		slices.Sort(names)
		lines = append(lines, html.EscapeString(strings.Join(lo.Map(names, func(name string, _ int) string {
			return fmt.Sprintf("%s=%v", name, reply.Params[name])
		}), ", ")))
	}
	if reply.StatusCode != 0 && (reply.StatusCode < 200 || reply.StatusCode > 299) {
		lines = append(lines, fmt.Sprintf(textHumanReplyStatus, reply.StatusCode))
	}
	if reply.MaskedFields > 0 {
		lines = append(lines, fmt.Sprintf(textHumanReplyMasked, reply.MaskedFields))
	}
	if reply.Truncated {
		lines = append(lines, textHumanReplyCut)
	}
	if inFile {
		lines = append(lines, textHumanReplyInFile)
	}
	if reply.RequestId != "" {
		lines = append(lines, fmt.Sprintf(textHumanReplyRequest, html.EscapeString(reply.RequestId)))
	}
	return strings.Join(lines, "\n")
}

// prettyJson — JSON с отступами; не JSON — как есть.
func prettyJson(data json.RawMessage) string {
	var buf bytes.Buffer
	if json.Indent(&buf, data, "", "  ") != nil {
		return string(data)
	}
	return buf.String()
}
