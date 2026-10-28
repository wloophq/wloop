package slack

import (
	"context"
	"fmt"
	"testing"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
)

// BenchmarkPushPass is one socket burst at working-day scale: 500 stored
// threads of 10 messages, then one reply to each of 200 of them admitted
// and touched in one pass.
func BenchmarkPushPass(b *testing.B) {
	f := newFixture(b)
	f.login(b)
	f.knownUser(b, other, false)
	var threads []dslack.Thread
	for i := range 500 {
		th := dslack.Thread{Conversation: chan1, TS: fmt.Sprintf("1700%06d.000000", i*100)}
		for j := range 10 {
			m := dslack.Message{TS: fmt.Sprintf("1700%06d.%06d", i*100, j), User: me, Text: "stored"}
			if j > 0 {
				m.ThreadTS = th.TS
			}
			th.Messages = append(th.Messages, m)
			th.LatestReply, th.ReplyCount = m.TS, j
		}
		threads = append(threads, th)
	}
	f.seed(b, dslack.Facts{Threads: threads})
	ctx := context.Background()
	b.ResetTimer()
	for n := range b.N {
		for i := range 200 {
			th := threads[i]
			ts := fmt.Sprintf("1700%06d.%06d", i*100, 100+n)
			f.p.deliver(ctx, delta(b, event(fmt.Sprintf("Ev%d-%d", n, i), evMsg(chan1, "channel", other, ts, "reply",
				"thread_ts", th.TS, "parent_user_id", me))))
		}
		f.flush(b)
	}
}

// BenchmarkDroppedDelta is the firehose's common case: a bot post in a
// conversation with nothing admitted, which moves the hwm only.
func BenchmarkDroppedDelta(b *testing.B) {
	f := newFixture(b)
	f.login(b)
	ctx := context.Background()
	b.ResetTimer()
	for n := range b.N {
		f.p.deliver(ctx, delta(b, event(fmt.Sprintf("Ev%d", n), evMsg(chan1, "channel", "", fmt.Sprintf("1700%06d.000100", n), "alert",
			"subtype", "bot_message", "bot_id", "B0001"))))
	}
}
