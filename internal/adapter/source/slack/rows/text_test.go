package slackrows

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

func TestPlainReadsLikeAPerson(t *testing.T) {
	users := map[string]sqlite.UserRow{"U000AAA": {ID: "U000AAA", Name: "ana", DisplayName: "Ana"}}
	for in, want := range map[string]string{
		"<@U000AAA> and <@U000BBB|bob>":                     "@Ana and @bob",
		"<@U000CCC> left":                                   "@U000CCC left",
		"see <#C000AAA|general> and <#C000BBB>":             "see #general and #C000BBB",
		"<!here> <!subteam^S000AAA|@ops> <!channel>":        "@here @ops @channel",
		"<https://example.test/a|the doc> <https://x.test>": "the doc https://x.test",
		"a &lt;b&gt; &amp; c":                               "a <b> & c",
	} {
		if got := plain(in, users); got != want {
			t.Errorf("plain(%q) = %q, want %q", in, got, want)
		}
	}
	if got := firstLine("\n  \n first \nsecond", 80); got != "first" {
		t.Errorf("first line %q", got)
	}
	long := strings.Repeat("é", 100)
	if got := firstLine(long, TitleRunes); len([]rune(got)) != TitleRunes || !strings.HasSuffix(got, "…") {
		t.Errorf("cut %q", got)
	}
}
