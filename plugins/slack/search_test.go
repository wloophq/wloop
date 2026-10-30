package slack

import (
	"context"
	"fmt"
	"github.com/wloophq/wloop/plugins/slack/store"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
)

const outside = "C0009"

// match is a search.messages match in a public channel the viewer is not in.
func match(conv, user, at, threadTS, text string) map[string]any {
	link := fmt.Sprintf("https://example.slack.com/archives/%s/p%s", conv, strings.ReplaceAll(at, ".", ""))
	if threadTS != "" {
		link += "?thread_ts=" + threadTS + "&cid=" + conv
	}
	return map[string]any{"type": "message", "user": user, "ts": at, "text": text, "permalink": link,
		"channel": map[string]any{"id": conv, "name": "elsewhere", "is_channel": true}}
}

// searchPages answers search.messages with pages of matches, the same for every query.
func (f *fixture) searchPages(pages ...[]any) {
	f.api.Handle("search.messages", func(v url.Values) any {
		n, _ := strconv.Atoi(v.Get("page"))
		if n < 1 || n > len(pages) {
			return map[string]any{"messages": map[string]any{"matches": []any{}, "paging": map[string]any{"page": n, "pages": len(pages)}}}
		}
		return map[string]any{"messages": map[string]any{"matches": pages[n-1], "total": 0,
			"paging": map[string]any{"page": n, "pages": len(pages)}}}
	})
}

// threads answers conversations.replies with the messages given by thread ts;
// an unknown ts is a lone message mentioning the viewer.
func (f *fixture) threads(byTS map[string][]any) {
	f.api.Handle("conversations.replies", func(v url.Values) any {
		if ms, ok := byTS[v.Get("ts")]; ok {
			return map[string]any{"messages": ms}
		}
		return map[string]any{"messages": []any{map[string]any{"type": "message", "user": other, "ts": v.Get("ts"), "text": "<@" + me + "> hi"}}}
	})
}

func TestSearchFindsNonMemberMentions(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.searchPages([]any{
		match(outside, other, ts(6), ts(5), "<@"+me+"> can you check"),
		match(outside, other, ts(3), "", "<@"+me+"> lone ask"),
	})
	f.threads(map[string][]any{ts(5): {
		map[string]any{"type": "message", "user": other, "ts": ts(5), "text": "parent", "thread_ts": ts(5), "reply_count": 1, "latest_reply": ts(6)},
		map[string]any{"type": "message", "user": other, "ts": ts(6), "text": "<@" + me + "> can you check", "thread_ts": ts(5)},
	}})
	ctx := context.Background()
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	q := f.api.Calls("search.messages")[0]
	if !strings.HasPrefix(q.Get("query"), "<@"+me+"> after:") || q.Get("sort") != "timestamp" || q.Get("sort_dir") != "desc" {
		t.Fatalf("search args %v", q)
	}
	via := map[string]string{}
	for _, m := range f.stored(t, outside) {
		via[m.TS] = m.Via
	}
	if via[ts(6)] != "search" || via[ts(3)] != "search" || len(via) != 3 {
		t.Fatalf("stored with via %v", via)
	}
	for _, object := range []string{dslack.ThreadObject(outside, ts(5)), dslack.ThreadObject(outside, ts(3))} {
		if o, ok := f.object(t, object); !ok || !o.Watched {
			t.Fatalf("%s not admitted: %+v", object, o)
		}
	}
	rows, err := f.db.Slack().Conversations(ctx, []string{outside})
	if err != nil || len(rows) != 1 || rows[0].Kind != string(dslack.KindChannel) || rows[0].IsMember {
		t.Fatalf("conversation %+v %v", rows, err)
	}
	if n := f.p.slack.Metrics.snapshot().DeltasByVia["search"]; n != 2 {
		t.Fatalf("search deltas %d", n)
	}
	if cur, _ := f.db.Slack().Cursor(ctx, store.SlackDiscoverCursor); cur != ts(6) {
		t.Fatalf("cursor %q", cur)
	}
}

func TestSearchStopsAtTheCursor(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	ctx := context.Background()
	if err := f.db.Slack().SetCursor(ctx, store.SlackDiscoverCursor, ts(600)); err != nil {
		t.Fatal(err)
	}
	mention := "<@" + me + "> ping"
	f.searchPages(
		[]any{match(outside, other, ts(1000), "", mention), match(outside, other, ts(900), "", mention)},
		[]any{match(outside, other, ts(500), "", mention), match(outside, other, ts(100), "", mention)},
		[]any{match(outside, other, ts(50), "", mention)},
	)
	f.threads(nil)
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	var pages []string
	for _, c := range f.api.Calls("search.messages") {
		pages = append(pages, c.Get("page"))
	}
	if !slices.Equal(pages, []string{"1", "2"}) {
		t.Fatalf("pages read %v", pages)
	}
	var stored []string
	for _, m := range f.stored(t, outside) {
		stored = append(stored, m.TS)
	}
	if !slices.Equal(stored, []string{ts(500), ts(900), ts(1000)}) {
		t.Fatalf("stored %v", stored)
	}
	if cur, _ := f.db.Slack().Cursor(ctx, store.SlackDiscoverCursor); cur != ts(1000) {
		t.Fatalf("cursor %q", cur)
	}
}

func TestSearchEveryFiveMinutes(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.searchPages([]any{})
	f.p.slack.Metrics.Socket(hello(f.now))
	ctx := context.Background()
	searches := func() int { return len(f.api.Calls("search.messages")) }
	if _, _, err := f.p.ingestJob(ctx); err != nil || searches() != 1 {
		t.Fatalf("first pass: %d searches, %v", searches(), err)
	}
	f.now = f.now.Add(2 * time.Minute)
	if _, _, err := f.p.ingestJob(ctx); err != nil || searches() != 1 {
		t.Fatalf("2 min later: %d searches, %v", searches(), err)
	}
	if _, err := f.p.search(ctx, "reconnect"); err != nil || searches() != 2 {
		t.Fatalf("catch-up: %d searches, %v", searches(), err)
	}
	f.now = f.now.Add(4 * time.Minute)
	if _, _, err := f.p.ingestJob(ctx); err != nil || searches() != 2 {
		t.Fatalf("4 min after the catch-up: %d searches, %v", searches(), err)
	}
	f.now = f.now.Add(time.Minute)
	if _, _, err := f.p.ingestJob(ctx); err != nil || searches() != 3 {
		t.Fatalf("5 min after the catch-up: %d searches, %v", searches(), err)
	}
}

func TestCatchUpSearchAdmitsIntoTheInbox(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.searchPages([]any{match(outside, other, ts(7), "", "<@"+me+"> see this"), match(chan1, "U0000000003", ts(4), "", "no mention here")})
	f.threads(nil)
	ctx := context.Background()
	n, err := f.p.search(ctx, "reconnect")
	if err != nil || n != 1 {
		t.Fatalf("search: %d %v", n, err)
	}
	if got := f.pendingObjects(); !slices.Equal(got, []string{dslack.ThreadObject(outside, ts(7))}) {
		t.Fatalf("inbox %v", got)
	}
	f.flush(t)
	if ms := f.stored(t, outside); len(ms) != 1 || ms[0].Via != "search" {
		t.Fatalf("stored %+v", ms)
	}
	if cur, _ := f.db.Slack().Cursor(ctx, store.SlackDiscoverCursor); cur != "" {
		t.Fatalf("the catch-up moved the cursor to %q before a pass saved its facts", cur)
	}
	if n, err := f.p.search(ctx, "reconnect"); err != nil || n != 0 {
		t.Fatalf("a second search admitted %d again: %v", n, err)
	}
}

func TestSearchSkipsWhenLimited(t *testing.T) {
	f := newFixture(t)
	f.slack.Client.Limits = &slackapi.Limiter{PerMinute: map[string]int{"search.messages": 1}}
	f.build(t)
	f.login(t)
	f.searchPages([]any{})
	ctx := context.Background()
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := f.p.search(ctx, "reconnect"); err != nil || n != 0 {
		t.Fatalf("catch-up search: %d %v", n, err)
	}
	f.now = f.now.Add(SearchEvery)
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(f.api.Calls("search.messages")); n != 1 {
		t.Fatalf("%d searches with the budget spent", n)
	}
	if got := strings.Count(f.logs.String(), SkipSearchLimited); got != 2 {
		t.Fatalf("budget reason logged %d times: %s", got, f.logs)
	}
}

func TestSearchQueriesEachTerm(t *testing.T) {
	f := newFixture(t, withConfig(func(i *config.SlackIngest) { i.Terms = []string{"deploy", "on call"} }))
	f.login(t)
	f.searchPages([]any{})
	if _, err := f.p.search(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	var qs []string
	for _, c := range f.api.Calls("search.messages") {
		q, _, _ := strings.Cut(c.Get("query"), " after:")
		qs = append(qs, q)
	}
	if !slices.Equal(qs, []string{"<@" + me + ">", "deploy", `"on call"`}) {
		t.Fatalf("queries %q", qs)
	}
}

func TestCatchUpMatchesSurviveALostInbox(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.searchPages([]any{match(outside, other, ts(1000), "", "<@"+me+"> newer"), match(outside, other, ts(7), "", "<@"+me+"> see this")})
	f.threads(nil)
	ctx := context.Background()
	if n, err := f.p.search(ctx, "reconnect"); err != nil || n != 2 {
		t.Fatalf("search: %d %v", n, err)
	}
	f.build(t)
	f.login(t)
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	if ms := f.stored(t, outside); len(ms) != 2 || ms[0].TS != ts(7) {
		t.Fatalf("after a restart that lost the inbox: %+v", ms)
	}
}

func TestFailedSearchWaitsAndWarns(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.api.Handle("search.messages", func(url.Values) any { return map[string]any{"ok": false, "error": "missing_scope"} })
	ctx := context.Background()
	searches := func() int { return len(f.api.Calls("search.messages")) }
	for range 4 {
		if _, _, err := f.p.ingestJob(ctx); err != nil {
			t.Fatal(err)
		}
		f.now = f.now.Add(time.Minute)
	}
	if n := searches(); n != 1 {
		t.Fatalf("%d searches in 4 min of failures", n)
	}
	if h, _ := f.health(t); !warned(h, "mention search fails") {
		t.Fatalf("warnings %v", h.Warnings)
	}
	f.now = f.now.Add(time.Minute)
	f.searchPages([]any{})
	if _, _, err := f.p.ingestJob(ctx); err != nil || searches() != 2 {
		t.Fatalf("5 min later: %d searches, %v", searches(), err)
	}
	if h, _ := f.health(t); warned(h, "mention search") {
		t.Fatalf("still warned after a search succeeded: %v", h.Warnings)
	}
}

func TestFailedSearchKeepsTheCatchUp(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	watchedThread(t, f, w)
	f.api.Handle("search.messages", func(url.Values) any { return map[string]any{"ok": false, "error": "missing_scope"} })
	if err := f.p.poll.catchUp(context.Background(), ReasonDropped); err != nil {
		t.Fatalf("catch-up: %v", err)
	}
	if s := f.p.slack.Metrics.snapshot(); s.LastCatchUp == nil || s.LastCatchUp.Reason != ReasonDropped {
		t.Fatalf("last catch-up %+v", s.LastCatchUp)
	}
	if !strings.Contains(f.logs.String(), "missing_scope") {
		t.Fatalf("the search error was not logged: %s", f.logs)
	}
	if h, _ := f.health(t); !warned(h, "mention search fails") {
		t.Fatalf("warnings %v", h.Warnings)
	}
}
