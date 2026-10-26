package slack

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/config"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/link"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type ledgerWithLog struct {
	*coreitems.Ledger
}

func (l ledgerWithLog) Log(ctx context.Context, u unit.Unit, since time.Time) ([]coreitems.LogEntry, error) {
	return l.Store.Log(ctx, u, since)
}

type askerAgent struct {
	turns.Agent
	asker turns.Asker
}

func (a askerAgent) Asker(string, map[turns.Tier]string) (turns.Asker, error) { return a.asker, nil }

type answerFunc func(turns.Ask) ([]byte, error)

func (f answerFunc) Ask(_ context.Context, a turns.Ask) (turns.Answer, error) {
	out, err := f(a)
	return turns.Answer{JSON: out, Model: "scripted"}, err
}

func extractionAnswer(t *testing.T, origin string, segments []map[string]string) []byte {
	t.Helper()
	it := map[string]any{"ref": "n1", "kind": "request", "summary": "review the PR", "origin_ts": origin, "requester": other,
		"assignees": []string{me}, "assignment_ts": origin, "assignment_basis": "explicit_mention", "waiting_on": []string{me},
		"state": "open", "resolution": "none", "evidence_ts": "", "evidence_kind": "none", "confidence": "high", "ambiguity": "",
		"links": []any{}, "context": "", "options": []any{}, "deadline": "", "mode": "headless", "place": map[string]string{"context": "", "why": ""}}
	segs := []map[string]any{}
	for _, sg := range segments {
		seg := map[string]any{"brief": json.RawMessage(`{"unchanged": false, "goal": "b", "context": "", "kind": "request", "risk": "low", "risk_why": "", "open_questions": [], "tags": {"domain": [], "audience": [], "concern": []}}`)}
		for k, v := range sg {
			seg[k] = v
		}
		segs = append(segs, seg)
	}
	out, err := json.Marshal(map[string]any{"brief": json.RawMessage(`{"unchanged": false, "goal": "b", "context": "", "kind": "request", "risk": "low", "risk_why": "", "open_questions": [], "tags": {"domain": [], "audience": [], "concern": []}}`), "viewer_role": "assignee", "started_by_viewer": false, "items": []any{it},
		"verdict": "waits_on_viewer", "verdict_why": "w", "closure_posted": false, "segments": segs,
		"links": []map[string]string{{"item": "n1", "target": "https://github.com/acme/widget/pull/527", "kind": "review", "evidence_ts": origin}}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestExtractionJobOverTheStore runs the module's job over a real store: a channel thread and
// a DM become items, the DM is segmented first, the PR URL becomes a link, and both objects
// are watched for extraction.
func TestExtractionJobOverTheStore(t *testing.T) {
	f := newFixture(t)
	ledger := &coreitems.Ledger{Store: f.db.Items(), Links: f.db.Links()}
	f.deps.Turns = &turns.Service{Items: ledgerWithLog{ledger}}
	f.deps.Clock = segClock{time.Now()}
	var prompts []string
	f.deps.Agents = map[string]turns.Agent{f.cfg.Agent: askerAgent{asker: answerFunc(func(a turns.Ask) ([]byte, error) {
		prompts = append(prompts, a.Prompt)
		if strings.Contains(a.Prompt, "Direct message") {
			return extractionAnswer(t, ts(5), []map[string]string{{"ref": "new", "from_ts": ts(5), "to_ts": "", "topic": "the PR"}}), nil
		}
		return extractionAnswer(t, ts(1), nil), nil
	})}}
	f.build(t)
	f.login(t)
	f.now = time.Now().Add(-time.Hour)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true},
			{ID: dm1, Kind: dslack.KindIM, IsMember: true, DMUser: other}},
		Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(2), ReplyCount: 1, Messages: []dslack.Message{
			{TS: ts(1), User: other, Text: "<@U0001> review please"}, {TS: ts(2), ThreadTS: ts(1), User: me, Text: "on it"}}}},
		DMs: []dslack.DMWindow{{Conversation: dm1, Latest: ts(5), Messages: []dslack.Message{{TS: ts(5), User: other, Text: "the PR?"}}}},
	})
	jobs := f.p.extraction(f.cfg)
	if len(jobs) != 1 || jobs[0].Name != extract.JobName || jobs[0].Every != extract.JobEvery || jobs[0].Timeout != extract.JobTimeout {
		t.Fatalf("jobs %+v", jobs)
	}
	summary, skipped, err := jobs[0].Run(context.Background())
	if err != nil || skipped != "" || len(prompts) != 2 {
		t.Fatalf("run: %q %q %v; %d calls", summary, skipped, err, len(prompts))
	}
	ctx := context.Background()
	thread := dslack.ThreadUnit(team, chan1, ts(1))
	its, err := ledger.Items(ctx, thread)
	if err != nil || len(its) != 1 || its[0].Provenance != "extracted" {
		t.Fatalf("thread items %+v %v", its, err)
	}
	seg, err := f.p.src.segments.UnitsFor(ctx, dm1, ts(5))
	if err != nil || len(seg) != 1 {
		t.Fatalf("DM segments %v %v", seg, err)
	}
	if dmItems, _ := ledger.Items(ctx, seg[0]); len(dmItems) != 1 {
		t.Fatalf("DM items %+v", dmItems)
	}
	links, err := f.db.Links().From(ctx, thread)
	if err != nil || !slices.ContainsFunc(links, func(l link.Link) bool { return l.To.Key == "acme/widget#527" && l.Item == its[0].ID }) {
		t.Fatalf("links %+v %v", links, err)
	}
	for _, obj := range []string{dslack.ThreadObject(chan1, ts(1)), dslack.DMObject(dm1)} {
		if o, ok := f.object(t, obj); !ok || !o.Watched || !slices.Contains(o.Reasons, string(dslack.ReasonExtraction)) {
			t.Errorf("%s: %+v", obj, o)
		}
	}
	hints, err := extract.Store{DB: f.db.Extraction()}.Hints(ctx, thread)
	if err != nil || hints[its[0].ID].Mode != "headless" {
		t.Fatalf("hints %+v %v", hints, err)
	}
	if w := f.p.slack.Metrics.snapshot().Warnings; len(w) != 0 {
		t.Fatalf("warnings with a closed breaker %v", w)
	}
}

func TestParseTarget(t *testing.T) {
	f := newFixture(t)
	for target, want := range map[string]string{
		"https://github.com/acme/widget/pull/527": "github:acme/widget#527",
		"github:acme/widget#527":                  "github:acme/widget#527",
		"slack:T0001/C0001/1700000001.000100":     "slack:T0001/C0001/1700000001.000100",
		"https://example.test/x":                  "",
		"jira:ABC-1":                              "",
	} {
		u, ok := f.p.parseTarget(target)
		if got := u.String(); (want == "") == ok || (ok && got != want) {
			t.Errorf("%s: %v %v, want %q", target, u, ok, want)
		}
	}
}

func TestExtractionWithoutAgentOrStore(t *testing.T) {
	f := newFixture(t)
	jobs := f.p.extraction(f.cfg)
	if _, skipped, err := jobs[0].Run(context.Background()); err != nil || skipped != extract.SkipNoAgent {
		t.Fatalf("no agent: %q %v", skipped, err)
	}
	bare, err := Module(f.cfg, f.deps, Slack{})
	if err != nil || len(bare.Jobs) != 1 {
		t.Fatalf("module jobs %+v %v", bare.Jobs, err)
	}
	if nostore := (&plugin{}).extraction(f.cfg); nostore != nil {
		t.Fatalf("a job without a store: %+v", nostore)
	}
}

type mutClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func threadAnswer(t *testing.T, ref, origin, evidence string) []byte {
	t.Helper()
	state, resolution, kind := "open", "none", "none"
	if evidence != "" {
		state, resolution, kind = "resolved", "done", "message"
	}
	it := map[string]any{"ref": ref, "kind": "request", "summary": "review the PR", "origin_ts": origin, "requester": other,
		"assignees": []string{me}, "assignment_ts": origin, "assignment_basis": "explicit_mention", "waiting_on": []string{},
		"state": state, "resolution": resolution, "evidence_ts": evidence, "evidence_kind": kind, "confidence": "high", "ambiguity": "",
		"links": []any{}, "context": "", "options": []any{}, "deadline": "", "mode": "none", "place": map[string]string{"context": "", "why": ""}}
	out, err := json.Marshal(map[string]any{"brief": json.RawMessage(`{"unchanged": false, "goal": "b", "context": "", "kind": "request", "risk": "low", "risk_why": "", "open_questions": [], "tags": {"domain": [], "audience": [], "concern": []}}`), "viewer_role": "assignee", "started_by_viewer": false, "items": []any{it},
		"verdict": "resolved", "verdict_why": "w", "closure_posted": false, "segments": []any{}, "links": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestUnwatchKeepsUserFollow settles two extracted threads; the one the user follows stays watched.
func TestUnwatchKeepsUserFollow(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	base := time.Now()
	clock := &mutClock{now: base}
	f.deps.Clock = clock
	f.deps.Turns = &turns.Service{Items: ledgerWithLog{&coreitems.Ledger{Store: f.db.Items(), Links: f.db.Links()}}}
	settled := false
	f.deps.Agents = map[string]turns.Agent{f.cfg.Agent: askerAgent{asker: answerFunc(func(a turns.Ask) ([]byte, error) {
		origin, reply := ts(1), ts(2)
		if strings.Contains(a.Prompt, "second thread") {
			origin, reply = ts(10), ts(11)
		}
		if !settled {
			return threadAnswer(t, "n1", origin, ""), nil
		}
		return threadAnswer(t, "e"+strings.ReplaceAll(origin, ".", ""), origin, reply), nil
	})}}
	f.cfg.Items.Heat.WarmFor = config.Days(time.Hour)
	f.build(t)
	f.login(t)
	f.now = base.Add(-time.Hour)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads: []dslack.Thread{
			{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "first thread"}}},
			{Conversation: chan1, TS: ts(10), Messages: []dslack.Message{{TS: ts(10), User: other, Text: "second thread"}}}},
	})
	run := f.p.extraction(f.cfg)[0].Run
	if _, _, err := run(ctx); err != nil {
		t.Fatal(err)
	}
	read, err := f.p.track(ctx, dslack.ThreadUnit(team, chan1, ts(1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Save(ctx); err != nil {
		t.Fatal(err)
	}
	settled = true
	f.now = base
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{
		{Conversation: chan1, TS: ts(1), LatestReply: ts(2), ReplyCount: 1, Messages: []dslack.Message{{TS: ts(2), ThreadTS: ts(1), User: me, Text: "done"}}},
		{Conversation: chan1, TS: ts(10), LatestReply: ts(11), ReplyCount: 1, Messages: []dslack.Message{{TS: ts(11), ThreadTS: ts(10), User: me, Text: "done"}}}}})
	clock.set(base.Add(5 * time.Minute))
	if _, _, err := run(ctx); err != nil {
		t.Fatal(err)
	}
	clock.set(base.Add(3 * time.Hour))
	if _, _, err := run(ctx); err != nil {
		t.Fatal(err)
	}
	if o, _ := f.object(t, dslack.ThreadObject(chan1, ts(10))); o.Watched {
		t.Fatalf("the settled thread nobody follows is still watched: %+v", o)
	}
	if o, _ := f.object(t, dslack.ThreadObject(chan1, ts(1))); !o.Watched {
		t.Fatalf("the job dropped the user's follow: %+v", o)
	}
}

func TestUnknownConversationIsAnUnknownUnit(t *testing.T) {
	f := newFixture(t)
	_, err := extractionFacts{f.p.src.store}.Conversation(context.Background(), "C9999")
	if !errors.Is(err, dslack.ErrUnknownUnit) {
		t.Fatalf("unknown conversation: %v", err)
	}
}

func TestUntrackedFollowDoesNotKeepTheWatch(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	st := f.db.Slack()
	obj := dslack.ObjectRef{Conversation: chan1, TS: ts(1)}
	w := Watches{DB: st}
	for _, step := range []func() error{
		func() error { return w.Watch(ctx, obj, dslack.ReasonUserFollow) },
		func() error { return w.Unwatch(ctx, obj) },
		func() error { return w.Watch(ctx, obj, dslack.ReasonExtraction) },
		func() error { return keepFollows{w, st}.Unwatch(ctx, obj) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if o, _ := f.object(t, obj.Object()); o.Watched {
		t.Fatalf("an untracked follow kept the extraction watch: %+v", o)
	}
}
