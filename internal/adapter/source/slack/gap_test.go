package slack

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/slack/socket"
	"github.com/wloophq/wloop/internal/slack/socket/sockettest"
)

func TestReconnectRunsCatchUp(t *testing.T) {
	f := newFixture(t)
	srv := sockettest.New(t)
	f.slack.Open = srv.Open
	f.build(t)
	f.login(t)
	f.setBackfilled(t)
	r := &reasons{}
	f.p.poll.Search = r.search
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.mod.Run(ctx) }()
	srv.Hello(1)
	waitFor(t, "the start catch-up", func() bool { return r.count(ReasonStart) == 1 })
	srv.Drop()
	srv.Hello(1)
	waitFor(t, "the reconnect catch-up", func() bool { return r.count(ReasonReconnect) == 1 })
	markOverflow(t, f, r, 3)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	if got := r.list(); !slices.Equal(got, []string{ReasonStart, ReasonReconnect, ReasonOverflow}) {
		t.Fatalf("catch-ups %v", got)
	}
	if s := f.p.slack.Metrics.snapshot(); s.LastCatchUp == nil || s.LastCatchUp.Reason != ReasonOverflow {
		t.Fatalf("last catch-up %+v", s.LastCatchUp)
	}
}

// markOverflow marks an overflow and waits for the n-th catch-up. Catch-ups run one at a time
// and a pending reason is kept over a later one, so a catch-up the test did not expect comes
// before the overflow's.
func markOverflow(t *testing.T, f *fixture, r *reasons, n int) {
	t.Helper()
	f.p.onState(socket.State{Kind: socket.StateOverflow, Reason: "events_api"})
	waitFor(t, "the overflow catch-up", func() bool { return len(r.list()) >= n })
}

func TestOverflowRunsCatchUp(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.setBackfilled(t)
	r := f.polling(t)
	waitFor(t, "the start catch-up", func() bool { return r.count(ReasonStart) == 1 })
	f.p.onState(socket.State{Kind: socket.StateHello})
	f.p.onState(socket.State{Kind: socket.StateOverflow, Reason: "events_api"})
	waitFor(t, "the overflow catch-up", func() bool { return r.count(ReasonOverflow) == 1 })
}

func TestClockJumpRunsCatchUp(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.setBackfilled(t)
	c := &clocks{wall: t0}
	c.install(f.p.poll.gaps)
	r := f.polling(t)
	waitFor(t, "the start catch-up", func() bool { return r.count(ReasonStart) == 1 })
	f.p.onState(socket.State{Kind: socket.StateHello, At: t0})
	g := f.p.poll.gaps
	g.tick()
	c.advance(GapTick, GapTick)
	g.tick()
	c.advance(19*time.Minute, time.Second)
	g.tick()
	markOverflow(t, f, r, 2)
	if got := r.list(); !slices.Equal(got, []string{ReasonStart, ReasonOverflow}) {
		t.Fatalf("a 19 min drift ran a catch-up: %v", got)
	}
	c.advance(40*time.Minute, GapTick)
	g.tick()
	waitFor(t, "the clock jump catch-up", func() bool { return r.count(ReasonClockJump) == 1 })
}

func TestDownFiveMinutesRunsCatchUp(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.setBackfilled(t)
	c := &clocks{wall: t0}
	c.install(f.p.poll.gaps)
	r := f.polling(t)
	waitFor(t, "the start catch-up", func() bool { return r.count(ReasonStart) == 1 })
	g := f.p.poll.gaps
	f.p.onState(socket.State{Kind: socket.StateHello, At: t0})
	f.p.onState(socket.State{Kind: socket.StateDropped, At: t0})
	g.tick()
	for range 12 {
		c.advance(GapTick, GapTick)
		g.tick()
	}
	waitFor(t, "the down catch-up", func() bool { return r.count(ReasonDown) == 1 })
	for range 4 {
		c.advance(GapTick, GapTick)
		g.tick()
	}
	markOverflow(t, f, r, 3)
	if got := r.list(); !slices.Equal(got, []string{ReasonStart, ReasonDown, ReasonOverflow}) {
		t.Fatalf("catch-ups %v", got)
	}
}

func TestStartRunsCatchUp(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	f.seed(t, dslack.Facts{DMs: []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{{TS: ts(1), User: other, Text: "hi"}}}}})
	if err := f.db.Slack().MarkSeen(context.Background(), dm1, ts(700)); err != nil {
		t.Fatal(err)
	}
	w.post(dm1, other, ts(1), "hi")
	w.post(dm1, other, ts(650), "lost at the last stop")
	w.post(dm1, other, ts(800), "while stopped")
	f.setBackfilled(t)
	r := f.polling(t)
	waitFor(t, "the start catch-up", func() bool { return r.count(ReasonStart) == 1 })
	calls := f.api.Calls("conversations.history")
	if len(calls) != 1 || calls[0].Get("oldest") != tsMinus(ts(700), StartOverlap) {
		t.Fatalf("history %v", calls)
	}
	if n := len(f.api.Calls("conversations.list")); n != 0 {
		t.Fatalf("a backfilled store backfilled again: %d list calls", n)
	}
	f.flush(t)
	var got []string
	for _, m := range f.stored(t, dm1) {
		got = append(got, m.TS)
	}
	if !slices.Equal(got, []string{ts(1), ts(650), ts(800)}) {
		t.Fatalf("stored %v", got)
	}
}
