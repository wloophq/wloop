package slack_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/slacktest"
)

func BenchmarkHistoryPage200(b *testing.B) {
	fake := slacktest.New(b)
	msgs := make([]any, 200)
	for i := range msgs {
		msgs[i] = map[string]any{"type": "message", "user": "U000", "ts": fmt.Sprintf("1700000000.%06d", i), "text": "some text of an ordinary length for a message", "reply_count": 3, "latest_reply": "1700000100.000000"}
	}
	fake.Handle("conversations.history", func(url.Values) any { return map[string]any{"messages": msgs, "has_more": true} })
	c := &slack.Client{BaseURL: fake.URL, Tokens: staticToken(fakeToken), Limits: &slack.Limiter{PerMinute: map[string]int{"conversations.history": 1 << 30}}}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.History(ctx, "C000", slack.PageArgs{Limit: 200}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLimiterWait(b *testing.B) {
	l := &slack.Limiter{PerMinute: map[string]int{"m": 1 << 30}}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_ = l.Wait(ctx, "m")
	}
}
