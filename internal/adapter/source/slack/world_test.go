package slack

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

// world is fake Slack's state: messages by conversation, served through
// conversations.history and conversations.replies as Slack pages them.
type world struct {
	mu   sync.Mutex
	msgs map[string][]map[string]any
	// page caps a history page below the asked limit.
	page int
	// noLatestReply leaves latest_reply out of history parents.
	noLatestReply bool
	// ignoreDMSearch answers a DM search with channel messages too.
	ignoreDMSearch bool
	// queries are the search.messages queries received.
	queries []string
}

func newWorld(f *fixture) *world {
	w := &world{msgs: map[string][]map[string]any{}}
	f.api.Handle("conversations.history", w.history)
	f.api.Handle("conversations.replies", w.replies)
	f.api.Handle("search.messages", w.search)
	return w
}

// post adds a message by user in conv; a thread ts makes it a reply.
func (w *world) post(conv, user, at, text string, threadTS ...string) {
	m := map[string]any{"type": "message", "user": user, "ts": at, "text": text}
	if len(threadTS) > 0 {
		m["thread_ts"] = threadTS[0]
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs[conv] = append(w.msgs[conv], m)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func parentOf(m map[string]any) bool {
	t := str(m, "thread_ts")
	return t == "" || t == str(m, "ts")
}

// withThread is a parent with its reply_count and latest_reply.
func (w *world) withThread(conv string, m map[string]any, latest bool) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	n, last := 0, ""
	for _, r := range w.msgs[conv] {
		if !parentOf(r) && str(r, "thread_ts") == str(m, "ts") {
			n++
			if dslack.CompareTS(str(r, "ts"), last) > 0 {
				last = str(r, "ts")
			}
		}
	}
	if n > 0 {
		out["thread_ts"] = str(m, "ts")
		out["reply_count"] = n
		if latest {
			out["latest_reply"] = last
		}
	}
	return out
}

func (w *world) history(v url.Values) any {
	w.mu.Lock()
	defer w.mu.Unlock()
	conv, oldest, latest := v.Get("channel"), v.Get("oldest"), v.Get("latest")
	inclusive := v.Get("inclusive") == "true"
	var all []map[string]any
	for _, m := range w.msgs[conv] {
		at := str(m, "ts")
		if !parentOf(m) {
			continue
		}
		if c := dslack.CompareTS(at, oldest); oldest != "" && (c < 0 || c == 0 && !inclusive) {
			continue
		}
		if c := dslack.CompareTS(at, latest); latest != "" && (c > 0 || c == 0 && !inclusive) {
			continue
		}
		all = append(all, w.withThread(conv, m, !w.noLatestReply))
	}
	slices.SortFunc(all, func(a, b map[string]any) int { return dslack.CompareTS(str(b, "ts"), str(a, "ts")) })
	return w.paged(all, v)
}

func (w *world) replies(v url.Values) any {
	w.mu.Lock()
	defer w.mu.Unlock()
	conv, at := v.Get("channel"), v.Get("ts")
	var parent map[string]any
	var all []map[string]any
	for _, m := range w.msgs[conv] {
		switch {
		case str(m, "ts") == at && parentOf(m):
			parent = w.withThread(conv, m, true)
		case str(m, "thread_ts") == at:
			all = append(all, m)
		}
	}
	if parent == nil {
		return map[string]any{"ok": false, "error": "thread_not_found"}
	}
	slices.SortFunc(all, func(a, b map[string]any) int { return dslack.CompareTS(str(a, "ts"), str(b, "ts")) })
	return w.paged(append([]map[string]any{parent}, all...), v)
}

func (w *world) paged(all []map[string]any, v url.Values) any {
	size, _ := strconv.Atoi(v.Get("limit"))
	if size <= 0 || w.page > 0 && w.page < size {
		size = max(w.page, 1)
	}
	from, _ := strconv.Atoi(v.Get("cursor"))
	to := min(from+size, len(all))
	out := map[string]any{"messages": all[min(from, len(all)):to], "has_more": to < len(all)}
	if to < len(all) {
		out["response_metadata"] = map[string]any{"next_cursor": strconv.Itoa(to)}
	}
	return out
}

// reasons records the reason of every catch-up through the poller's Search.
type reasons struct {
	mu  sync.Mutex
	got []string
}

func (r *reasons) search(_ context.Context, reason string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, reason)
	return 0, nil
}

func (r *reasons) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func (r *reasons) count(reason string) int {
	n := 0
	for _, g := range r.list() {
		if g == reason {
			n++
		}
	}
	return n
}

// clocks are the gap detector's fake wall and monotonic clocks.
type clocks struct {
	mu   sync.Mutex
	wall time.Time
	mono time.Duration
}

func (c *clocks) advance(wall, mono time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(wall)
	c.mono += mono
}

func (c *clocks) install(g *gaps) {
	g.wall = func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.wall }
	g.mono = func() time.Duration { c.mu.Lock(); defer c.mu.Unlock(); return c.mono }
}

// polling runs the poller loop until the test ends, recording catch-ups.
func (f *fixture) polling(t *testing.T) *reasons {
	t.Helper()
	r := &reasons{}
	f.p.poll.Search = r.search
	f.p.poll.tick = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.p.poll.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return r
}

func (f *fixture) setBackfilled(t testing.TB) {
	t.Helper()
	if err := f.db.Slack().SetCursor(context.Background(), CursorBackfill, "done"); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) hwm(t testing.TB, conv string) string {
	t.Helper()
	rows, err := f.db.Slack().Conversations(context.Background(), []string{conv})
	if err != nil || len(rows) != 1 {
		t.Fatalf("conversation %s: %v %v", conv, rows, err)
	}
	return rows[0].HWM
}

// search answers a DM search with every DM message of the world, newest first.
func (w *world) search(v url.Values) any {
	w.mu.Lock()
	defer w.mu.Unlock()
	q := v.Get("query")
	w.queries = append(w.queries, q)
	var matches []map[string]any
	if strings.HasPrefix(q, DMSearch+" ") {
		for conv, ms := range w.msgs {
			im := strings.HasPrefix(conv, "D")
			if !im && !w.ignoreDMSearch {
				continue
			}
			for _, m := range ms {
				link := "https://acme.slack.com/archives/" + conv + "/p" + strings.Replace(str(m, "ts"), ".", "", 1)
				if !parentOf(m) {
					link += "?thread_ts=" + str(m, "thread_ts")
				}
				matches = append(matches, map[string]any{"ts": str(m, "ts"), "user": str(m, "user"), "text": str(m, "text"),
					"permalink": link, "channel": map[string]any{"id": conv, "is_im": im, "is_channel": !im}})
			}
		}
	}
	slices.SortFunc(matches, func(a, b map[string]any) int { return dslack.CompareTS(str(b, "ts"), str(a, "ts")) })
	return map[string]any{"messages": map[string]any{"matches": matches, "total": len(matches),
		"paging": map[string]any{"page": 1, "pages": 1}}}
}

func (w *world) searched() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.queries)
}
