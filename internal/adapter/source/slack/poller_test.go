package slack

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
)

// threads seeds n watched threads of chan1, each a parent with one reply,
// parents at ts(10*i) and replies one second later.
func threads(t *testing.T, f *fixture, w *world, n int) {
	t.Helper()
	var fs dslack.Facts
	for i := 1; i <= n; i++ {
		p, r := ts(10*i), ts(10*i+1)
		fs.Threads = append(fs.Threads, dslack.Thread{Conversation: chan1, TS: p, LatestReply: r, ReplyCount: 1,
			Messages: []dslack.Message{{TS: p, User: me, Text: "parent"}, {TS: r, ThreadTS: p, User: other, Text: "reply"}}})
		w.post(chan1, me, p, "parent")
		w.post(chan1, other, r, "reply", p)
	}
	f.seed(t, fs)
}

// pass runs the ingestion job once every watched object is due, cold ones included.
func (f *fixture) pass(t *testing.T) (summary, skipped string) {
	t.Helper()
	f.now = f.now.Add(PollCold + time.Minute)
	summary, skipped, err := ingest.RunJob(context.Background(), f.mod.Ingest, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return summary, skipped
}

func TestProbeCostsOneCallPerChannel(t *testing.T) {
	f := newFixture(t)
	f.namedChannel(t, chan1)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	threads(t, f, w, 10)
	w.post(chan1, other, ts(52), "news", ts(50))
	f.pass(t)
	if got := f.calls(apiMethods...); got["conversations.history"] != 1 || got["conversations.replies"] != 1 || len(got) != 3 || got["search.messages"] != 1 {
		t.Fatalf("calls %v", got)
	}
	h := f.api.Calls("conversations.history")[0]
	if h.Get("oldest") != ts(10) || h.Get("latest") != ts(100) || h.Get("inclusive") != "true" {
		t.Fatalf("history window %v", h)
	}
	if o, _ := f.object(t, dslack.ThreadObject(chan1, ts(50))); o.Version != ts(52) {
		t.Fatalf("moved thread version %q", o.Version)
	}
	f.pass(t)
	if got := f.calls(apiMethods...); got["conversations.history"] != 2 || got["conversations.replies"] != 1 {
		t.Fatalf("an unmoved pass read threads: %v", got)
	}
}

func TestProbeFallsBackWithoutLatestReply(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	w.noLatestReply = true
	threads(t, f, w, 3)
	w.post(chan1, other, ts(22), "news", ts(20))
	f.pass(t)
	var probes, reads int
	for _, c := range f.api.Calls("conversations.replies") {
		if c.Get("limit") == "1" {
			probes++
		} else {
			reads++
		}
	}
	if n := len(f.api.Calls("conversations.history")); n != 1 || probes != 3 || reads != 1 {
		t.Fatalf("history %d, probes %d, reads %d", n, probes, reads)
	}
	if o, _ := f.object(t, dslack.ThreadObject(chan1, ts(20))); o.Version != ts(22) {
		t.Fatalf("moved thread version %q", o.Version)
	}
	f.pass(t)
	if n := len(f.api.Calls("conversations.history")); n != 1 {
		t.Fatalf("history after the fallback: %d calls", n)
	}
	if n := strings.Count(f.logs.String(), "latest_reply"); n != 1 {
		t.Fatalf("fallback logged %d times:\n%s", n, f.logs)
	}
}

func TestProbeNotesTheMovedThread(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	threads(t, f, w, 2)
	from := f.log.Gen()
	f.pass(t)
	if c := f.log.Since(from, port.TopicSourceEvents); !c.Empty() {
		t.Fatalf("an unmoved pass noted %+v", c)
	}
	w.post(chan1, other, ts(12), "news", ts(10))
	from = f.log.Gen()
	f.pass(t)
	c := f.log.Since(from, port.TopicSourceEvents)
	if want := []unit.Unit{dslack.ThreadUnit(team, chan1, ts(10))}; !slices.Equal(c.Units, want) {
		t.Fatalf("noted %+v", c)
	}
}

func TestPollSkipsWhenLimited(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	threads(t, f, w, 2)
	l := &slackapi.Limiter{PerMinute: map[string]int{"conversations.history": 1}}
	if err := l.Wait(context.Background(), "conversations.history"); err != nil {
		t.Fatal(err)
	}
	f.slack.Client.Limits = l
	if summary, skipped := f.pass(t); summary != "skipped" || skipped != SkipBudget+"conversations.history" {
		t.Fatalf("limited pass: %q %q", summary, skipped)
	}
	if got := f.calls(apiMethods...); len(got) != 0 {
		t.Fatalf("a skipped pass called %v", got)
	}
	f.slack.Client.Limits = &slackapi.Limiter{}
	if _, skipped := f.pass(t); skipped != "" {
		t.Fatalf("the next pass skipped: %q", skipped)
	}
	if n := len(f.api.Calls("conversations.history")); n != 1 {
		t.Fatalf("the next pass made %d history calls", n)
	}
}

func TestProbeMatchesTheStoredStubsWhenNothingMoved(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	w := newWorld(f)
	threads(t, f, w, 4)
	f.seed(t, dslack.Facts{
		Threads: []dslack.Thread{{Conversation: "C0002", TS: ts(5), Messages: []dslack.Message{{TS: ts(5), User: other, Text: "<@" + me + ">", EditedTS: ts(6)}}}},
		DMs:     []dslack.DMWindow{{Conversation: dm1, Messages: []dslack.Message{{TS: ts(7), User: other, Text: "hi"}}}},
	})
	w.mu.Lock()
	w.msgs["C0002"] = append(w.msgs["C0002"], map[string]any{"type": "message", "user": other, "ts": ts(5), "text": "<@" + me + ">",
		"edited": map[string]any{"ts": ts(6)}})
	w.mu.Unlock()
	if err := f.db.Slack().SetWatched(context.Background(), dslack.ThreadObject(chan1, ts(900)), true, "user-follow"); err != nil {
		t.Fatal(err)
	}
	objects := []string{dslack.ThreadObject(chan1, ts(10)), dslack.ThreadObject(chan1, ts(20)), dslack.ThreadObject(chan1, ts(30)),
		dslack.ThreadObject(chan1, ts(40)), dslack.ThreadObject("C0002", ts(5)), dslack.DMObject(dm1), dslack.ThreadObject(chan1, ts(900))}
	old, err := f.p.src.Stubs(context.Background(), objects)
	if err != nil {
		t.Fatal(err)
	}
	probed, err := f.p.poll.stubs(context.Background(), objects)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(old, probed) {
		t.Fatalf("probe %+v, stored %+v", probed, old)
	}
}
