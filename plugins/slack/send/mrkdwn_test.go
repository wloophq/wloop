package send_test

import (
	"os"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/send"
)

func TestMrkdwn(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"plain", "hello world", "hello world"},
		{"bold stars", "a **bold** word", "a *bold* word"},
		{"bold underscores", "a __bold__ word", "a *bold* word"},
		{"italic star", "an *italic* word", "an _italic_ word"},
		{"italic underscore", "an _italic_ word", "an _italic_ word"},
		{"bold and italic", "**b** and *i*", "*b* and _i_"},
		{"bold italic", "***both***", "_*both*_"},
		{"strikethrough", "~~gone~~", "~gone~"},
		{"snake case", "use my_var_name here", "use my_var_name here"},
		{"arithmetic stars", "2 * 3 * 4", "2 * 3 * 4"},
		{"link", "see [the docs](https://example.com/a?b=1&c=2)", "see <https://example.com/a?b=1&amp;c=2|the docs>"},
		{"mailto link", "[mail](mailto:a@b.c)", "<mailto:a@b.c|mail>"},
		{"special link stays text", "[hi](!here) [x](!subteam^S1)", "[hi](!here) [x](!subteam^S1)"},
		{"mention link stays text", "[x](@U123) [y](#C123) [z](javascript:x)", "[x](@U123) [y](#C123) [z](javascript:x)"},
		{"link with bold text", "[**x**](https://e.com)", "<https://e.com|*x*>"},
		{"autolink", "<https://example.com>", "<https://example.com>"},
		{"bare url", "go to https://example.com/x_y_z now", "go to https://example.com/x_y_z now"},
		{"escape", "a < b && c > d", "a &lt; b &amp;&amp; c &gt; d"},
		{"user mention", "hi <@U123> and <@U456|bob>", "hi <@U123> and <@U456|bob>"},
		{"channel and special mentions", "<#C123|general> <!here> <!subteam^S1>", "<#C123|general> <!here> <!subteam^S1>"},
		{"inline code", "run `a **b** <c> & d` now", "run `a **b** &lt;c&gt; &amp; d` now"},
		{"bullets", "- one\n* two\n+ three\n  - nested", "• one\n• two\n• three\n  • nested"},
		{"bullet with emphasis", "- **one** item", "• *one* item"},
		{"ordered list", "1. first\n2. second", "1. first\n2. second"},
		{"heading", "## Status", "*Status*"},
		{"quote", "> quoted *text*", "> quoted _text_"},
		{
			"code block",
			"before\n```go\nx := a**b < c\n- not a list\n```\nafter **b**",
			"before\n```\nx := a**b &lt; c\n- not a list\n```\nafter *b*",
		},
		{"unclosed code block", "```\na *b*", "```\na *b*\n```"},
		{"unclosed bold", "**open", "**open"},
		{"empty", "", ""},
		{"placeholder runes in the input", "a \uE0000\uE001 \uE002b\uE002 `c`", "a 0 b `c`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := send.Mrkdwn(c.in); got != c.want {
				t.Fatalf("Mrkdwn(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestMrkdwnSourceHasNoRawPrivateUseRunes(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("mrkdwn.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range string(src) {
		if r >= 0xE000 && r <= 0xF8FF {
			t.Fatalf("mrkdwn.go holds a raw private-use rune U+%04X at byte %d; write it as an escape", r, i)
		}
	}
}
