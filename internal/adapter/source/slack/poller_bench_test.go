package slack

import (
	"context"
	"fmt"
	"testing"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

// BenchmarkProbe is one Stubs over 500 watched threads in 50 channels,
// none moved: 50 conversations.history calls against fake Slack.
func BenchmarkProbe(b *testing.B) {
	f := newFixture(b)
	f.login(b)
	w := newWorld(f)
	var fs dslack.Facts
	var objects []string
	for c := range 50 {
		conv := fmt.Sprintf("C%04d", c+1)
		for i := range 10 {
			p, r := fmt.Sprintf("17000%05d.000100", i*10), fmt.Sprintf("17000%05d.000100", i*10+1)
			fs.Threads = append(fs.Threads, dslack.Thread{Conversation: conv, TS: p, LatestReply: r, ReplyCount: 1,
				Messages: []dslack.Message{{TS: p, User: me, Text: "parent"}, {TS: r, ThreadTS: p, User: other, Text: "reply"}}})
			w.post(conv, me, p, "parent")
			w.post(conv, other, r, "reply", p)
			objects = append(objects, dslack.ThreadObject(conv, p))
		}
	}
	f.seed(b, fs)
	f.slack.Client.Limits.PerMinute = map[string]int{"conversations.history": 1 << 30}
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if _, err := f.p.poll.stubs(ctx, objects); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCatchUp is one catch-up over 100 admitted conversations with
// 20 messages each since the high-water mark: 50 DMs, all admitted, and
// 50 channels of firehose with one mention each.
func BenchmarkCatchUp(b *testing.B) {
	ctx := context.Background()
	for range b.N {
		b.StopTimer()
		f := newFixture(b)
		f.login(b)
		f.knownUser(b, other, false)
		w := newWorld(f)
		f.slack.Client.Limits.PerMinute = map[string]int{"conversations.history": 1 << 30, "search.messages": 1 << 30}
		var fs dslack.Facts
		for c := range 100 {
			conv := fmt.Sprintf("C%04d", c+1)
			if c < 50 {
				conv = fmt.Sprintf("D%04d", c+1)
				fs.DMs = append(fs.DMs, dslack.DMWindow{Conversation: conv, Messages: []dslack.Message{{TS: ts(1), User: other, Text: "hi"}}})
			} else {
				fs.Threads = append(fs.Threads, dslack.Thread{Conversation: conv, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "<@" + me + ">"}}})
			}
			w.post(conv, other, ts(1), "seed")
			for i := 2; i <= 21; i++ {
				text := "firehose"
				if i == 21 {
					text = "<@" + me + "> ping"
				}
				w.post(conv, other, ts(i), text)
			}
		}
		f.seed(b, fs)
		b.StartTimer()
		if err := f.p.poll.catchUp(ctx, ReasonReconnect); err != nil {
			b.Fatal(err)
		}
	}
}
