package slack

import (
	"context"
	"net/url"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
)

func TestFirstStartBackfillsSince(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.SlackIngest) { c.Since = "2023-11-14" }))
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	f.api.Handle("conversations.list", func(v url.Values) any {
		if v.Get("types") != "im,mpim" {
			return map[string]any{"ok": false, "error": "invalid_types"}
		}
		return map[string]any{"channels": []any{
			map[string]any{"id": dm1, "is_im": true, "user": other},
			map[string]any{"id": "G0001", "is_group": true, "is_mpim": true, "is_private": true},
		}}
	})
	w.post(dm1, other, ts(1), "hi")
	w.post(dm1, other, ts(2), "a thread")
	w.post(dm1, me, ts(3), "a reply", ts(2))
	w.post("G0001", other, ts(4), "group hello")
	w.post("C0002", other, ts(5), "a question")
	w.post("C0002", me, ts(6), "my answer", ts(5))
	w.post("C0003", other, ts(7), "<@"+me+"> ping")
	var mu sync.Mutex
	var queries []string
	f.api.Handle("search.messages", func(v url.Values) any {
		mu.Lock()
		queries = append(queries, v.Get("query"))
		mu.Unlock()
		var matches []any
		switch v.Get("query") {
		case "from:<@" + me + "> after:2023-11-13":
			matches = []any{map[string]any{"ts": ts(6), "user": me, "text": "my answer",
				"permalink": "https://acme.slack.com/archives/C0002/p1700000006000100?thread_ts=" + ts(5),
				"channel":   map[string]any{"id": "C0002", "is_channel": true}}}
		case "<@" + me + "> after:2023-11-13":
			matches = []any{
				map[string]any{"ts": ts(7), "user": other, "text": "<@" + me + "> ping",
					"permalink": "https://acme.slack.com/archives/C0003/p1700000007000100",
					"channel":   map[string]any{"id": "C0003", "is_channel": true}},
				map[string]any{"ts": "1699000000.000100", "user": other, "text": "too old",
					"channel": map[string]any{"id": "C0009", "is_channel": true}},
			}
		}
		return map[string]any{"messages": map[string]any{"matches": matches, "total": len(matches),
			"paging": map[string]any{"page": 1, "pages": 1}}}
	})
	r := f.polling(t)
	waitFor(t, "the backfill", func() bool { f.flush(t); return f.p.slack.Metrics.snapshot().Backfill.Done })
	want := map[string][]string{dm1: {ts(1), ts(2), ts(3)}, "G0001": {ts(4)}, "C0002": {ts(5), ts(6)}, "C0003": {ts(7)}}
	for conv, tss := range want {
		var got []string
		for _, m := range f.stored(t, conv) {
			got = append(got, m.TS)
			if m.TS != ts(5) && m.Via != string(dslack.ViaCatchUp) {
				t.Fatalf("%s/%s via %q", conv, m.TS, m.Via)
			}
		}
		if !slices.Equal(got, tss) {
			t.Fatalf("%s stored %v, want %v", conv, got, tss)
		}
	}
	if _, ok := f.object(t, dslack.ThreadObject("C0009", "1699000000.000100")); ok {
		t.Fatal("a match older than since was read")
	}
	for _, c := range f.api.Calls("conversations.history") {
		if c.Get("oldest") != "1699920000.000000" {
			t.Fatalf("history from %s", c.Get("oldest"))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(queries, []string{"from:<@" + me + "> after:2023-11-13", "<@" + me + "> after:2023-11-13"}) {
		t.Fatalf("queries %v", queries)
	}
	if v, err := f.db.Slack().Cursor(context.Background(), CursorBackfill); err != nil || v == "" {
		t.Fatalf("backfill cursor %q %v", v, err)
	}
	b := f.p.slack.Metrics.snapshot().Backfill
	if b.Conversations != 2 || b.Threads != 3 || b.Deltas != 6 {
		t.Fatalf("backfill %+v", b)
	}
	if len(r.list()) != 0 {
		t.Fatalf("the backfill ran a catch-up too: %v", r.list())
	}
}

func TestBackfillRunsOnce(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	newWorld(f)
	f.setBackfilled(t)
	r := f.polling(t)
	waitFor(t, "the start catch-up", func() bool { return r.count(ReasonStart) == 1 })
	if n := len(f.api.Calls("conversations.list")) + len(f.api.Calls("search.messages")); n != 0 {
		t.Fatalf("a second start backfilled: %d calls", n)
	}
	if f.p.slack.Metrics.snapshot().Backfill.Done {
		t.Fatal("the catch-up reported a backfill")
	}
}

func TestBackfillCursorWaitsForTheInbox(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.SlackIngest) { c.Since = "2023-11-14" }))
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	f.api.Handle("conversations.list", func(url.Values) any {
		return map[string]any{"channels": []any{map[string]any{"id": dm1, "is_im": true, "user": other}}}
	})
	w.post(dm1, other, ts(1), "hi")
	f.polling(t)
	waitFor(t, "the backfill waiting on the inbox", func() bool { return f.p.slack.Metrics.snapshot().Backfill.Draining == 1 })
	cursor := func() string {
		v, err := f.db.Slack().Cursor(context.Background(), CursorBackfill)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := cursor(); v != "" {
		t.Fatalf("the cursor was set with deltas in the inbox: %q", v)
	}
	if b := f.p.slack.Metrics.snapshot().Backfill; b.Done || b.Conversations != 1 || b.Deltas != 1 {
		t.Fatalf("progress %+v", b)
	}
	f.flush(t)
	waitFor(t, "the cursor", func() bool { return cursor() != "" })
}

func TestBackfillStoppedBeforeTheInboxLeavesNoCursor(t *testing.T) {
	f := newFixture(t, withConfig(func(c *config.SlackIngest) { c.Since = "2023-11-14" }))
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	f.api.Handle("conversations.list", func(url.Values) any {
		return map[string]any{"channels": []any{map[string]any{"id": dm1, "is_im": true, "user": other}}}
	})
	w.post(dm1, other, ts(1), "hi")
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- f.p.poll.backfill(ctx) }()
	waitFor(t, "the DM queued", func() bool { return len(f.pendingObjects()) == 1 })
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("a stopped backfill finished")
	}
	if v, _ := f.db.Slack().Cursor(context.Background(), CursorBackfill); v != "" {
		t.Fatalf("cursor %q", v)
	}
}
