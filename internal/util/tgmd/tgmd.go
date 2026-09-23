// Package tgmd готовит Markdown ответа модели к отправке в Telegram: переводит
// в Telegram HTML (b, i, s, code, pre, a, blockquote) и режет под лимит длины
// сообщения. HTML, а не MarkdownV2: в MarkdownV2 любой неэкранированный символ из
// длинного списка роняет всё сообщение, а в HTML экранируются только < > &.
package tgmd

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/samber/lo"
)

// MessageLimit — лимит длины текста сообщения Telegram (в символах, после разбора разметки).
const MessageLimit = 4096

const fence = "```"

var (
	reHeading = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	reList    = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	reQuote   = regexp.MustCompile(`^>\s?(.*)$`)
	reRule    = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	reLang    = regexp.MustCompile(`^[A-Za-z0-9_+-]+$`)

	reLink   = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	reBold   = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	reBold2  = regexp.MustCompile(`__([^_\n]+?)__`)
	reStrike = regexp.MustCompile(`~~([^~\n]+?)~~`)
	// *курсив*: звёздочка не внутри слова/числа (2*3*4 — не курсив);
	// _курсив_ не поддерживаем — ломал бы имена вида get_service_snapshot
	reItalic = regexp.MustCompile(`(^|[^\p{L}\p{N}*])\*([^*\s][^*\n]*?)\*`)
)

// ToHTML переводит Markdown в Telegram HTML. Незнакомая разметка остаётся текстом.
func ToHTML(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])

		switch {
		case strings.HasPrefix(trimmed, fence):
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, fence))
			end := i + 1
			for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), fence) {
				end++
			}
			code := html.EscapeString(strings.Join(lines[i+1:min(end, len(lines))], "\n"))
			if reLang.MatchString(lang) {
				out = append(out, `<pre><code class="language-`+lang+`">`+code+`</code></pre>`)
			} else {
				out = append(out, "<pre>"+code+"</pre>")
			}
			i = end + 1

		case isTableLine(trimmed):
			// таблиц Telegram не умеет — моноширинным блоком они хотя бы читаются
			end := i
			for end < len(lines) && isTableLine(strings.TrimSpace(lines[end])) {
				end++
			}
			out = append(out, "<pre>"+html.EscapeString(strings.Join(lines[i:end], "\n"))+"</pre>")
			i = end

		case reQuote.MatchString(trimmed):
			var quote []string
			for i < len(lines) && reQuote.MatchString(strings.TrimSpace(lines[i])) {
				quote = append(quote, inline(reQuote.FindStringSubmatch(strings.TrimSpace(lines[i]))[1]))
				i++
			}
			out = append(out, "<blockquote>"+strings.Join(quote, "\n")+"</blockquote>")

		case reHeading.MatchString(trimmed):
			out = append(out, "<b>"+inline(reHeading.FindStringSubmatch(trimmed)[1])+"</b>")
			i++

		case reRule.MatchString(trimmed):
			out = append(out, "──────────")
			i++

		case reList.MatchString(lines[i]):
			m := reList.FindStringSubmatch(lines[i])
			out = append(out, m[1]+"• "+inline(m[2]))
			i++

		default:
			out = append(out, inline(lines[i]))
			i++
		}
	}

	return strings.Join(out, "\n")
}

func isTableLine(s string) bool {
	return len(s) > 1 && strings.HasPrefix(s, "|") && strings.HasSuffix(s, "|")
}

// inline — разметка внутри строки; содержимое `кода` не форматируется.
func inline(s string) string {
	parts := strings.Split(s, "`")
	// нечётное число обратных кавычек: последняя непарная остаётся текстом
	if len(parts)%2 == 0 {
		parts[len(parts)-2] += "`" + parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}

	var b strings.Builder
	for i, part := range parts {
		if i%2 == 1 {
			b.WriteString("<code>" + html.EscapeString(part) + "</code>")
			continue
		}
		b.WriteString(formatText(part))
	}
	return b.String()
}

func formatText(s string) string {
	s = html.EscapeString(s)
	s = reLink.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = reBold.ReplaceAllString(s, "<b>$1</b>")
	s = reBold2.ReplaceAllString(s, "<b>$1</b>")
	s = reStrike.ReplaceAllString(s, "<s>$1</s>")
	s = reItalic.ReplaceAllString(s, "$1<i>$2</i>")
	return s
}

// Split режет Markdown на части не длиннее limit символов: по абзацам, длинный
// абзац — по строкам, блок кода — не разрывая разметку (каждая часть блока
// заново открывается и закрывается). Разметка в HTML только укорачивает текст,
// поэтому лимит по исходному Markdown держит и лимит Telegram.
func Split(md string, limit int) []string {
	var pieces []string
	for _, block := range splitBlocks(md) {
		pieces = append(pieces, fitBlock(block, limit)...)
	}

	return pack(pieces, "\n\n", limit)
}

// splitBlocks делит текст на абзацы и блоки кода.
func splitBlocks(md string) []string {
	var blocks, cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}

	inFence := false
	for _, line := range strings.Split(strings.TrimSpace(md), "\n") {
		isFence := strings.HasPrefix(strings.TrimSpace(line), fence)

		switch {
		case inFence:
			cur = append(cur, line)
			if isFence {
				inFence = false
				flush()
			}
		case isFence:
			flush()
			cur = append(cur, line)
			inFence = true
		case strings.TrimSpace(line) == "":
			flush()
		default:
			cur = append(cur, line)
		}
	}
	flush()

	return blocks
}

// fitBlock режет блок, если он длиннее limit.
func fitBlock(block string, limit int) []string {
	if utf8.RuneCountInString(block) <= limit {
		return []string{block}
	}

	lines := strings.Split(block, "\n")
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), fence) {
		return pack(hardSplitLines(lines, limit), "\n", limit)
	}

	header := lines[0]
	body := lines[1:]
	if len(body) > 0 && strings.HasPrefix(strings.TrimSpace(body[len(body)-1]), fence) {
		body = body[:len(body)-1]
	}

	// каждая часть: header + \n + тело + \n + ```
	bodyLimit := limit - utf8.RuneCountInString(header) - len(fence) - 2
	return lo.Map(pack(hardSplitLines(body, bodyLimit), "\n", bodyLimit), func(part string, _ int) string {
		return header + "\n" + part + "\n" + fence
	})
}

// pack склеивает части через sep, пока результат не длиннее limit.
func pack(parts []string, sep string, limit int) []string {
	if len(parts) == 0 {
		return nil
	}

	result := []string{parts[0]}
	for _, p := range parts[1:] {
		last := &result[len(result)-1]
		if utf8.RuneCountInString(*last)+utf8.RuneCountInString(sep)+utf8.RuneCountInString(p) <= limit {
			*last += sep + p
			continue
		}
		result = append(result, p)
	}
	return result
}

// hardSplitLines режет строки длиннее limit по символам.
func hardSplitLines(lines []string, limit int) []string {
	var result []string
	for _, line := range lines {
		runes := []rune(line)
		for len(runes) > limit {
			result = append(result, string(runes[:limit]))
			runes = runes[limit:]
		}
		result = append(result, string(runes))
	}
	return result
}
