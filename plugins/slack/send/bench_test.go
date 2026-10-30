package send_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/send"
	slack "github.com/wloophq/wloop/plugins/slack/slackapi"
)

func BenchmarkMrkdwn(b *testing.B) {
	var md strings.Builder
	for i := range 40 {
		fmt.Fprintf(&md, "- **item %d** with `code <x>` and [a link](https://e.com/%d?a=1&b=2) and _it_ <@U%03d>\n", i, i, i)
	}
	md.WriteString("```go\nfunc main() { a < b }\n```\n")
	in := md.String()
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		send.Mrkdwn(in)
	}
}

// BenchmarkResolveHandleColdDirectory pages a 5000-user workspace (25 users.list pages) per op.
func BenchmarkResolveHandleColdDirectory(b *testing.B) {
	ws := newWorkspace()
	for i := range 5000 {
		ws.users = append(ws.users, map[string]any{"id": fmt.Sprintf("U9%05d", i), "name": fmt.Sprintf("user%d", i)})
	}
	f := newFixture(b, ws)
	pages := func(v url.Values) any {
		start := 0
		fmt.Sscan(v.Get("cursor"), &start)
		end := min(start+200, len(ws.users))
		next := ""
		if end < len(ws.users) {
			next = fmt.Sprint(end)
		}
		return map[string]any{"members": ws.users[start:end], "response_metadata": map[string]any{"next_cursor": next}}
	}
	f.slack.Handle("users.list", pages)
	limits := &slack.Limiter{PerMinute: map[string]int{"users.list": 1 << 30, "conversations.open": 1 << 30}}
	for b.Loop() {
		svc := &send.Service{Client: &slack.Client{BaseURL: f.slack.URL, Tokens: staticToken{fakeToken}, Limits: limits}}
		if _, err := svc.Resolve(context.Background(), send.Request{Target: "@alice"}); err != nil {
			b.Fatal(err)
		}
	}
}
