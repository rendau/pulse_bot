package tgmd

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToHTML(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want string
	}{
		{"escape", "a < b && c > d", "a &lt; b &amp;&amp; c &gt; d"},
		{"bold", "**caravan** лежит", "<b>caravan</b> лежит"},
		{"italic", "это *важно*", "это <i>важно</i>"},
		{"not italic inside numbers", "2*3*4", "2*3*4"},
		{"underscores untouched", "get_service_snapshot и resolve_service", "get_service_snapshot и resolve_service"},
		{"strike", "~~было~~", "<s>было</s>"},
		{"inline code not formatted", "см. `**raw** <x>`", "см. <code>**raw** &lt;x&gt;</code>"},
		{"unpaired backtick", "a ` b", "a ` b"},
		{"link", "[дашборд](https://grafana.local/d?a=1&b=2)", `<a href="https://grafana.local/d?a=1&amp;b=2">дашборд</a>`},
		{"heading", "## Итог", "<b>Итог</b>"},
		{"list", "- первый\n* второй\n  - вложенный", "• первый\n• второй\n  • вложенный"},
		{"quote", "> строка 1\n> строка 2", "<blockquote>строка 1\nстрока 2</blockquote>"},
		{"code block with lang", "```go\nif a < b {}\n```", `<pre><code class="language-go">if a &lt; b {}</code></pre>`},
		{"code block without lang", "```\n**x**\n```", "<pre>**x**</pre>"},
		{"unclosed code block", "```\nline", "<pre>line</pre>"},
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |", "<pre>| a | b |\n|---|---|\n| 1 | 2 |</pre>"},
		{"rule", "---", "──────────"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ToHTML(tt.md))
		})
	}
}

func TestSplit_Short(t *testing.T) {
	md := "Вывод.\n\n- факт 1\n- факт 2"
	assert.Equal(t, []string{md}, Split(md, 100))
}

func TestSplit_ByParagraphs(t *testing.T) {
	p1 := strings.Repeat("а", 60)
	p2 := strings.Repeat("б", 60)
	p3 := strings.Repeat("в", 30)

	parts := Split(p1+"\n\n"+p2+"\n\n"+p3, 100)

	assert.Equal(t, []string{p1, p2 + "\n\n" + p3}, parts)
}

func TestSplit_LongLine(t *testing.T) {
	parts := Split(strings.Repeat("я", 250), 100)

	require.Len(t, parts, 3)
	for _, p := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(p), 100)
	}
	assert.Equal(t, strings.Repeat("я", 250), strings.Join(parts, ""))
}

func TestSplit_CodeBlockKeepsFences(t *testing.T) {
	lines := make([]string, 0, 30)
	for range 30 {
		lines = append(lines, "log line 0123456789")
	}
	lines[5] = "" // пустая строка внутри кода не теряется
	md := "Логи:\n\n```text\n" + strings.Join(lines, "\n") + "\n```"

	parts := Split(md, 200)

	require.Greater(t, len(parts), 2)
	var body []string
	for _, p := range parts {
		assert.LessOrEqual(t, utf8.RuneCountInString(p), 200)
		if p == "Логи:" {
			continue
		}
		p = strings.TrimPrefix(p, "Логи:\n\n")
		require.True(t, strings.HasPrefix(p, "```text\n"), p)
		require.True(t, strings.HasSuffix(p, "\n```"), p)
		body = append(body, strings.TrimSuffix(strings.TrimPrefix(p, "```text\n"), "\n```"))
	}
	assert.Equal(t, strings.Join(lines, "\n"), strings.Join(body, "\n"))
}
