package slack

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/extract"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// answers makes the host's agent answer each extraction prompt with fn.
func (f *fixture) answers(fn func(prompt string) []byte) {
	f.host.AskFunc = func(_ context.Context, req src.AskRequest) (src.AskResult, error) {
		return src.AskResult{Text: string(fn(req.Prompt)), Model: "scripted"}, nil
	}
}

// pullAddress is a GitHub pull request's address, which the host's codecs
// resolve to its unit.
var pullAddress = regexp.MustCompile(`^https://github\.com/([^/\s]+)/([^/\s]+)/pull/([1-9][0-9]*)$`)

func resolvePull(address string) (unit.Unit, bool) {
	m := pullAddress.FindStringSubmatch(address)
	if m == nil {
		return unit.Unit{}, false
	}
	return unit.Unit{Provider: "github", Key: m[1] + "/" + m[2] + "#" + m[3]}, true
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

// TestExtractionJobOverTheStore runs the plugin's job over a real store: a channel thread and
// a DM become items, the DM is segmented first, the PR URL becomes a link, and both objects
// are watched for extraction.
func TestExtractionJobOverTheStore(t *testing.T) {
	f := newFixture(t)
	f.host.Resolve = resolvePull
	var prompts []string
	f.answers(func(prompt string) []byte {
		prompts = append(prompts, prompt)
		if strings.Contains(prompt, "Direct message") {
			return extractionAnswer(t, ts(5), []map[string]string{{"ref": "new", "from_ts": ts(5), "to_ts": "", "topic": "the PR"}})
		}
		return extractionAnswer(t, ts(1), nil)
	})
	f.login(t)
	f.now = t0.Add(-time.Hour)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true},
			{ID: dm1, Kind: dslack.KindIM, IsMember: true, DMUser: other}},
		Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(2), ReplyCount: 1, Messages: []dslack.Message{
			{TS: ts(1), User: other, Text: "<@U0001> review please"}, {TS: ts(2), ThreadTS: ts(1), User: me, Text: "on it"}}}},
		DMs: []dslack.DMWindow{{Conversation: dm1, Latest: ts(5), Messages: []dslack.Message{{TS: ts(5), User: other, Text: "the PR?"}}}},
	})
	if j, ok := manifestJob(t, JobExtract); !ok || time.Duration(j.Every) != extract.JobEvery || time.Duration(j.Timeout) != extract.JobTimeout {
		t.Fatalf("the manifest's job %+v", j)
	}
	f.now = t0
	summary, skipped, err := f.p.job.Run(context.Background())
	if err != nil || skipped != "" || len(prompts) != 2 {
		t.Fatalf("run: %q %q %v; %d calls", summary, skipped, err, len(prompts))
	}
	ctx := context.Background()
	thread := dslack.ThreadUnit(team, chan1, ts(1))
	its := f.host.Items(thread)
	if len(its) != 1 || its[0].Provenance != "extracted" {
		t.Fatalf("thread items %+v", its)
	}
	seg, err := f.p.src.segments.UnitsFor(ctx, dm1, ts(5))
	if err != nil || len(seg) != 1 {
		t.Fatalf("DM segments %v %v", seg, err)
	}
	if dmItems := f.host.Items(seg[0]); len(dmItems) != 1 {
		t.Fatalf("DM items %+v", dmItems)
	}
	links := f.host.Links()
	if !slices.ContainsFunc(links, func(l link.Link) bool {
		return l.From == thread && l.To.Key == "acme/widget#527" && l.Item == its[0].ID
	}) {
		t.Fatalf("links %+v", links)
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
	for target, want := range map[string]unit.Unit{
		"https://github.com/acme/widget/pull/527": {Key: "https://github.com/acme/widget/pull/527"},
		"https://example.test/x":                  {Key: "https://example.test/x"},
		"github:acme/widget#527":                  {Provider: "github", Key: "acme/widget#527"},
		"jira:ABC-1":                              {Provider: "jira", Key: "ABC-1"},
		"slack:T0001/C0001/1700000001.000100":     {Provider: "slack", Key: "T0001/C0001/1700000001.000100"},
	} {
		if u, ok := parseTarget(target); !ok || u != want {
			t.Errorf("%s: %v %v, want %v", target, u, ok, want)
		}
	}
	for _, target := range []string{"", "  ", "C0001/1700000001.000100", "https://acme.slack.com/archives/C0001/p1700000001000100", "nokey:", "a b:c"} {
		if u, ok := parseTarget(target); ok {
			t.Errorf("%q: %v", target, u)
		}
	}
}

func TestExtractionWithoutAgentOrStore(t *testing.T) {
	f := newFixture(t)
	cfg := f.cfg
	if _, skipped, err := newPlugin(cfg, host.Broker{}, f.db, f.slack, nil).job.Run(context.Background()); err != nil || skipped != extract.SkipNoAgent {
		t.Fatalf("no agent: %q %v", skipped, err)
	}
	h := f.host.Src()
	h.Ledger = nil
	if _, skipped, err := newPlugin(cfg, host.Broker{Host: h}, f.db, f.slack, nil).job.Run(context.Background()); err != nil || skipped != extract.SkipNoLedger {
		t.Fatalf("no ledger: %q %v", skipped, err)
	}
	if nostore := newPlugin(cfg, f.host.Broker(), nil, Slack{}, nil); nostore.job != nil {
		t.Fatalf("a job without a store: %+v", nostore.job)
	}
}

// manifestJob is the manifest's job name.
func manifestJob(t testing.TB, name string) (manifest.Job, bool) {
	t.Helper()
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range s.Jobs {
		if j.Name == name {
			return j, true
		}
	}
	return manifest.Job{}, false
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
	base := t0
	settled := false
	f.answers(func(prompt string) []byte {
		origin, reply := ts(1), ts(2)
		if strings.Contains(prompt, "second thread") {
			origin, reply = ts(10), ts(11)
		}
		if !settled {
			return threadAnswer(t, "n1", origin, "")
		}
		return threadAnswer(t, "e"+strings.ReplaceAll(origin, ".", ""), origin, reply)
	})
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
	f.now = base
	run := f.p.job.Run
	if _, _, err := run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.p.track(ctx, dslack.ThreadUnit(team, chan1, ts(1)), true); err != nil {
		t.Fatal(err)
	}
	settled = true
	f.now = base
	f.seed(t, dslack.Facts{Threads: []dslack.Thread{
		{Conversation: chan1, TS: ts(1), LatestReply: ts(2), ReplyCount: 1, Messages: []dslack.Message{{TS: ts(2), ThreadTS: ts(1), User: me, Text: "done"}}},
		{Conversation: chan1, TS: ts(10), LatestReply: ts(11), ReplyCount: 1, Messages: []dslack.Message{{TS: ts(11), ThreadTS: ts(10), User: me, Text: "done"}}}}})
	f.now = base.Add(5 * time.Minute)
	if _, _, err := run(ctx); err != nil {
		t.Fatal(err)
	}
	f.now = base.Add(3 * time.Hour)
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

func TestAFirstTurnsBriefWantReadsAWaitingThreadWithTheBriefIntentsFields(t *testing.T) {
	f := newFixture(t)
	f.host.Configured = src.Config{BriefIntent: &src.BriefIntent{ExtraSchema: json.RawMessage(`{"type":"object","properties":{"approach":{"type":"string"}}}`),
		FieldVersions: map[string]string{"approach": "f1-abc"}}}
	var schemas []string
	f.host.AskFunc = func(_ context.Context, req src.AskRequest) (src.AskResult, error) {
		schemas = append(schemas, string(req.Schema))
		return src.AskResult{Text: string(extractionAnswer(t, ts(1), nil)), Model: "scripted"}, nil
	}
	f.login(t)
	f.now = tsAt(1)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads:       []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "<@U0001> review please"}}}},
	})
	thread := dslack.ThreadUnit(team, chan1, ts(1))
	running := &Plugin{p: f.p}
	ctx := context.Background()
	if want, err := running.BriefWant(ctx, thread.Wire()); err != nil || want {
		t.Fatalf("a want before anything is waiting: %v %v", want, err)
	}
	f.now = tsAt(1).Add(10 * time.Second)
	if _, _, err := f.p.job.Run(ctx); err != nil || len(schemas) != 0 {
		t.Fatalf("a pass before quiet_for: %v, %d asks", err, len(schemas))
	}
	if want, err := running.BriefWant(ctx, thread.Wire()); err != nil || !want {
		t.Fatalf("a thread waiting to be read: %v %v", want, err)
	}
	if _, _, err := f.p.job.Run(ctx); err != nil || len(schemas) != 1 {
		t.Fatalf("the wanted pass: %v, %d asks", err, len(schemas))
	}
	if !strings.Contains(schemas[0], `"approach"`) {
		t.Fatalf("the extraction asked for no brief field of the intent: %s", schemas[0])
	}
	if bs := f.host.Briefs(); len(bs) != 1 || unit.Of(bs[0].Unit) != thread {
		t.Fatalf("briefs %+v", bs)
	}
	if _, err := (&Plugin{}).BriefWant(ctx, thread.Wire()); err == nil {
		t.Fatal("a plugin that is not running answered a want")
	}
}
