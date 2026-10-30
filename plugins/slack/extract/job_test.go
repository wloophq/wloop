package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/config"
	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/host/hosttest"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

const (
	jTeam   = "T000AAA"
	jViewer = "U000AAA"
	jOther  = "U000BBB"
	jChan   = "C000AAA"
	jDM     = "D000AAA"
)

var jT0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

type jobClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *jobClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *jobClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// fakeFacts is the facts of one team: threads in jChan, a DM in jDM.
type fakeFacts struct {
	convs   map[string]slack.Conversation
	msgs    map[string][]slack.Message // by conversation, ascending
	changes []slack.Change
	users   map[string]slack.User
	// convErrs fail that many Conversation calls before answering.
	convErrs int
}

func newFakeFacts() *fakeFacts {
	return &fakeFacts{
		convs: map[string]slack.Conversation{
			jChan: {ID: jChan, Name: "general", Kind: slack.KindChannel, IsMember: true},
			jDM:   {ID: jDM, Kind: slack.KindIM, IsMember: true, DMUser: jOther},
		},
		msgs: map[string][]slack.Message{},
		users: map[string]slack.User{
			jViewer: {ID: jViewer, Name: "ann", Email: "ann@example.test"},
			jOther:  {ID: jOther, Name: "bob"},
		},
	}
}

// post stores a message and its change; ts is "<seconds>.000000" from n.
func (f *fakeFacts) post(conv, ts, thread, user, text string, at time.Time) slack.Message {
	m := slack.Message{TS: ts, ThreadTS: thread, User: user, Text: text, Permalink: "https://example.test/" + ts}
	f.msgs[conv] = append(f.msgs[conv], m)
	slices.SortFunc(f.msgs[conv], func(a, b slack.Message) int { return slack.CompareTS(a.TS, b.TS) })
	f.change(conv, ts, thread, slack.ChangeNew, at)
	return m
}

func (f *fakeFacts) change(conv, ts, thread string, kind slack.ChangeKind, at time.Time) {
	f.changes = append(f.changes, slack.Change{Seq: int64(len(f.changes) + 1), Conversation: conv, TS: ts, ThreadTS: thread, Kind: kind, At: at})
}

func (f *fakeFacts) Messages(_ context.Context, u unit.Unit, after string, limit int) ([]slack.Message, error) {
	k, err := slack.ParseUnit(u)
	if err != nil || k.Segment != "" {
		return nil, fmt.Errorf("%w: %s", slack.ErrUnknownUnit, u)
	}
	var out []slack.Message
	for _, m := range f.msgs[k.Conversation] {
		if m.Anchor() == k.TS && slack.CompareTS(m.TS, after) > 0 {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", slack.ErrUnknownUnit, u)
	}
	return out, nil
}

func (f *fakeFacts) dm(_ context.Context, conv, from string) ([]slack.Message, error) {
	var out []slack.Message
	for _, m := range f.msgs[conv] {
		if slack.CompareTS(m.TS, from) >= 0 {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeFacts) Conversation(_ context.Context, id string) (slack.Conversation, error) {
	if f.convErrs > 0 {
		f.convErrs--
		return slack.Conversation{}, errors.New("database is locked")
	}
	c, ok := f.convs[id]
	if !ok {
		return slack.Conversation{}, fmt.Errorf("%w: conversation %s", slack.ErrUnknownUnit, id)
	}
	return c, nil
}

func (f *fakeFacts) Users(_ context.Context, ids []string) (map[string]slack.User, error) {
	out := map[string]slack.User{}
	for _, id := range ids {
		if u, ok := f.users[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

func (f *fakeFacts) UserByEmail(context.Context, string) (slack.User, bool, error) {
	return slack.User{}, false, nil
}

func (f *fakeFacts) Changes(_ context.Context, after int64, limit int) ([]slack.Change, error) {
	var out []slack.Change
	for _, c := range f.changes {
		if c.Seq > after {
			out = append(out, c)
		}
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeFacts) Viewer(context.Context) (string, string, error) { return jTeam, jViewer, nil }

type watchCall struct {
	obj    slack.ObjectRef
	reason slack.WatchReason
	off    bool
}

type fakeWatcher struct{ calls []watchCall }

func (w *fakeWatcher) Watch(_ context.Context, obj slack.ObjectRef, reason slack.WatchReason) error {
	w.calls = append(w.calls, watchCall{obj: obj, reason: reason})
	return nil
}

func (w *fakeWatcher) Unwatch(_ context.Context, obj slack.ObjectRef) error {
	w.calls = append(w.calls, watchCall{obj: obj, off: true})
	return nil
}

func segUnit(team, conv, id string) unit.Unit {
	return slack.Key{Team: team, Conversation: conv, Segment: id}.Unit()
}

// segments is the segment map over the store, recording each Apply in events.
type segments struct {
	store  *store.SegmentStore
	clock  *jobClock
	events *[]string
	spans  [][]slack.Span
}

func (s *segments) UnitsFor(ctx context.Context, conv, ts string) ([]unit.Unit, error) {
	rows, err := s.store.Covering(ctx, slack.Provider, conv, ts)
	var out []unit.Unit
	for _, r := range rows {
		out = append(out, segUnit(r.Team, r.Conversation, r.ID))
	}
	return out, err
}

func (s *segments) Range(ctx context.Context, u unit.Unit) (string, string, string, error) {
	k, err := slack.ParseUnit(u)
	if err != nil {
		return "", "", "", err
	}
	r, err := s.store.Segment(ctx, slack.Provider, k.Conversation, k.Segment)
	return r.Conversation, r.FromTS, r.ToTS, err
}

func (s *segments) Apply(ctx context.Context, team, conv string, spans []slack.Span) ([]unit.Unit, error) {
	*s.events = append(*s.events, "segments")
	s.spans = append(s.spans, spans)
	rows := make([]store.SegmentSpan, len(spans))
	for i, sp := range spans {
		rows[i] = store.SegmentSpan(sp)
	}
	got, err := s.store.Apply(ctx, slack.Provider, team, conv, rows, segUnit, s.clock.Now())
	if err != nil {
		return nil, err
	}
	out := make([]unit.Unit, len(got))
	for i, r := range got {
		out[i] = segUnit(r.Team, r.Conversation, r.ID)
	}
	return out, nil
}

// recordingLedger is the host's ledger through the broker; it records the
// ops of every Apply in events.
type recordingLedger struct {
	host   *hosttest.Host
	events *[]string
	ops    [][]items.Op
}

func (l *recordingLedger) Apply(ctx context.Context, prov items.Provenance, ops []items.Op) (host.Result, error) {
	*l.events = append(*l.events, "ops")
	l.ops = append(l.ops, ops)
	if prov != items.Extracted {
		return host.Result{}, fmt.Errorf("provenance %s", prov)
	}
	return l.host.Broker().Apply(ctx, prov, ops)
}

func (l *recordingLedger) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	return l.host.Broker().Items(ctx, u)
}

type jobAsker struct {
	mu    sync.Mutex
	asks  []host.Ask
	reply func(n int, a host.Ask) ([]byte, error)
	cost  *float64
}

func (a *jobAsker) Ask(_ context.Context, q host.Ask) (host.Answer, error) {
	a.mu.Lock()
	n := len(a.asks)
	a.asks = append(a.asks, q)
	a.mu.Unlock()
	out, err := a.reply(n, q)
	if err != nil {
		return host.Answer{CostUSD: a.cost}, err
	}
	return host.Answer{JSON: out, Model: "scripted", CostUSD: a.cost}, nil
}

func (a *jobAsker) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.asks)
}

type rig struct {
	t      testing.TB
	db     *store.DB
	facts  *fakeFacts
	asker  *jobAsker
	watch  *fakeWatcher
	segs   *segments
	ledger *recordingLedger
	clock  *jobClock
	cfg    config.Config
	events []string
	job    *Job
	logs   []string
}

func newRig(t testing.TB) *rig {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{t: t, db: db, facts: newFakeFacts(), watch: &fakeWatcher{}, clock: &jobClock{now: jT0}, cfg: config.Config{}}
	r.asker = &jobAsker{reply: func(int, host.Ask) ([]byte, error) { return answerJSON(t, nil, nil, nil, nil), nil }}
	r.segs = &segments{store: db.Segments(), clock: r.clock, events: &r.events}
	r.ledger = &recordingLedger{host: &hosttest.Host{Now: r.clock.Now}, events: &r.events}
	r.job = &Job{
		Facts: r.facts, Segments: r.segs, Writer: r.segs, DM: r.facts.dm, Ledger: r.ledger, Watcher: r.watch, Asker: r.asker,
		Parse: func(target string) (unit.Unit, bool) {
			key, ok := strings.CutPrefix(target, "github:")
			return unit.Unit{Provider: githubProvider, Key: key}, ok
		},
		Store:   Store{DB: db.Extraction(), Segments: db.Segments()},
		Clock:   r.clock,
		Cfg:     func() config.Config { return r.cfg },
		Log:     func(f string, a ...any) { r.logs = append(r.logs, fmt.Sprintf(f, a...)) },
		Prompts: func(context.Context) (*Prompts, error) { return testPrompts, nil },
	}
	return r
}

func (r *rig) run(at time.Time) (string, string) {
	r.t.Helper()
	r.clock.set(at)
	summary, skipped, err := r.job.Run(context.Background())
	if err != nil {
		r.t.Fatalf("run at %s: %v", at.Format(time.TimeOnly), err)
	}
	return summary, skipped
}

func ts(n int) string { return fmt.Sprintf("17000%05d.000000", n) }

func thread(anchor string) unit.Unit { return slack.ThreadUnit(jTeam, jChan, anchor) }

type obj = map[string]any

func jItem(ref, origin string, mods ...func(obj)) obj {
	it := obj{"ref": ref, "kind": "request", "summary": "Bob asks Ann for a review", "origin_ts": origin, "requester": jOther,
		"assignees": []string{jViewer}, "assignment_ts": origin, "assignment_basis": "explicit_mention", "waiting_on": []string{jViewer},
		"state": "open", "resolution": "none", "evidence_ts": "", "evidence_kind": "none", "confidence": "high", "ambiguity": "",
		"links": []any{}, "context": "", "options": []any{}, "deadline": "", "mode": "none", "place": obj{"context": "", "why": ""}}
	for _, m := range mods {
		m(it)
	}
	return it
}

func resolved(evidence string) func(obj) {
	return func(it obj) {
		it["state"], it["resolution"], it["evidence_ts"], it["evidence_kind"] = "resolved", "done", evidence, "message"
	}
}

// briefObj is an answer's brief of goal, the kind a request.
func briefObj(goal string) obj {
	return obj{"unchanged": false, "goal": goal, "context": "", "kind": "request", "risk": "low", "risk_why": "",
		"open_questions": []any{}, "tags": obj{"domain": []any{}, "audience": []any{}, "concern": []any{}}}
}

func answerJSON(t testing.TB, its, segs, links []obj, top obj) []byte {
	t.Helper()
	for _, sg := range segs {
		if _, ok := sg["brief"]; !ok {
			sg["brief"] = briefObj(fmt.Sprint(sg["topic"]))
		}
	}
	a := obj{"brief": briefObj("a brief"), "viewer_role": "assignee", "started_by_viewer": false, "items": orEmpty(its), "verdict": "waits_on_viewer",
		"verdict_why": "why", "closure_posted": false, "segments": orEmpty(segs), "links": orEmpty(links)}
	for k, v := range top {
		a[k] = v
	}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func orEmpty(xs []obj) []obj {
	if xs == nil {
		return []obj{}
	}
	return xs
}

func (r *rig) items(u unit.Unit) []items.Item {
	r.t.Helper()
	its, err := r.ledger.Items(context.Background(), u)
	if err != nil {
		r.t.Fatal(err)
	}
	return its
}

func TestDebouncePerUnit(t *testing.T) {
	r := newRig(t)
	a, b := thread(ts(1)), thread(ts(10))
	r.facts.post(jChan, ts(1), "", jOther, "first", jT0)
	r.facts.post(jChan, ts(2), ts(1), jOther, "second", jT0.Add(30*time.Second))
	r.facts.post(jChan, ts(3), ts(1), jOther, "third", jT0.Add(60*time.Second))
	r.facts.post(jChan, ts(10), "", jOther, "other unit", jT0)
	r.run(jT0.Add(61 * time.Second))
	if n := r.asker.calls(); n != 0 {
		t.Fatalf("%d calls before any unit was quiet", n)
	}
	r.run(jT0.Add(91 * time.Second))
	if n := r.asker.calls(); n != 1 || !strings.Contains(r.asker.asks[0].Prompt, "other unit") {
		t.Fatalf("after 91 s: %d calls; want only the quiet unit %s", n, b)
	}
	r.run(jT0.Add(151 * time.Second))
	if n := r.asker.calls(); n != 2 {
		t.Fatalf("after the burst was quiet: %d calls, want 2", n)
	}
	p := r.asker.asks[1].Prompt
	for _, text := range []string{"first", "second", "third"} {
		if !strings.Contains(p, text) {
			t.Errorf("the call for %s misses %q", a, text)
		}
	}
	r.run(jT0.Add(10 * time.Minute))
	if n := r.asker.calls(); n != 2 {
		t.Fatalf("a processed unit ran again: %d calls", n)
	}
}

func TestMaxWaitBoundsTheDelay(t *testing.T) {
	r := newRig(t)
	for i := range 12 {
		at := jT0.Add(time.Duration(i) * time.Minute)
		r.facts.post(jChan, ts(1+i), threadOf(i), jOther, fmt.Sprintf("msg %d", i), at)
		r.run(at.Add(time.Second))
		switch n := r.asker.calls(); {
		case i < 10 && n != 0:
			t.Fatalf("minute %d: %d calls before max_wait", i, n)
		case i == 10 && n != 1:
			t.Fatalf("minute %d: %d calls, want the max_wait run", i, n)
		}
	}
}

func threadOf(i int) string {
	if i == 0 {
		return ""
	}
	return ts(1)
}

func TestFailedCallKeepsTheCursor(t *testing.T) {
	r := newRig(t)
	u := thread(ts(1))
	r.facts.post(jChan, ts(1), "", jOther, "please review", jT0)
	r.asker.reply = func(n int, _ host.Ask) ([]byte, error) {
		if n == 0 {
			return nil, host.ErrUnavailable
		}
		return answerJSON(t, []obj{jItem("n1", ts(1))}, nil, nil, nil), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	st, err := r.job.Store.States(context.Background(), []unit.Unit{u})
	if err != nil || len(st) != 0 {
		t.Fatalf("a failed call saved state %+v %v", st, err)
	}
	r.run(jT0.Add(4 * time.Minute))
	if n := r.asker.calls(); n != 2 || !strings.Contains(r.asker.asks[1].Prompt, "new ") {
		t.Fatalf("retry: %d calls; prompt marks no new message", n)
	}
	if its := r.items(u); len(its) != 1 || its[0].ID != "e"+strings.ReplaceAll(ts(1), ".", "") {
		t.Fatalf("items after the retry %+v", its)
	}
	if st, _ := r.job.Store.States(context.Background(), []unit.Unit{u}); GoalOf(st[u].Brief) != "a brief" {
		t.Fatalf("state after the retry %+v", st)
	}
}

func TestDMSegmentsBeforeOps(t *testing.T) {
	r := newRig(t)
	r.facts.post(jDM, ts(1), "", jOther, "can you review the deck", jT0)
	r.facts.post(jDM, ts(2), "", jViewer, "sure", jT0)
	r.facts.post(jDM, ts(3), "", jOther, "also the PR please", jT0)
	r.asker.reply = func(n int, _ host.Ask) ([]byte, error) {
		if n == 0 {
			return answerJSON(t, []obj{jItem("n1", ts(1)), jItem("n2", ts(3))},
				[]obj{{"ref": "new", "from_ts": ts(1), "to_ts": ts(2), "topic": "the deck"}, {"ref": "new", "from_ts": ts(3), "to_ts": "", "topic": "the PR"}},
				[]obj{{"item": "n2", "target": "github:acme/widget#527", "kind": "review", "evidence_ts": ts(3)}}, nil), nil
		}
		return answerJSON(t, nil,
			[]obj{{"ref": "s1", "from_ts": ts(1), "to_ts": ts(2), "topic": "the deck"}, {"ref": "s2", "from_ts": ts(4), "to_ts": "", "topic": "the PR, again"}},
			nil, nil), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	if !slices.Equal(r.events, []string{"segments", "ops"}) {
		t.Fatalf("order %v, want the segments before the ops", r.events)
	}
	deck, pr := segUnit(jTeam, jDM, strings.ReplaceAll(ts(1), ".", "")), segUnit(jTeam, jDM, strings.ReplaceAll(ts(3), ".", ""))
	for _, op := range r.ledger.ops[0] {
		want := deck
		if op.Item == "e"+strings.ReplaceAll(ts(3), ".", "") {
			want = pr
		}
		if op.Unit != want || op.Anchor.Unit != want || (op.Evidence != nil && op.Evidence.Anchor.Unit != want) {
			t.Errorf("%s op on %s lands on %s / %s, want %s", op.Op, op.Item, op.Unit, op.Anchor.Unit, want)
		}
		if op.Op == items.OpLink && (op.To == nil || *op.To != (unit.Unit{Provider: githubProvider, Key: "acme/widget#527"}) ||
			!op.Anchor.At.Equal(slack.TSTime(ts(3)))) {
			t.Errorf("link op %+v: want the PR at the message time", op)
		}
	}
	if len(r.items(deck)) != 1 || len(r.items(pr)) != 1 {
		t.Fatalf("items deck %v pr %v", r.items(deck), r.items(pr))
	}
	st, err := r.job.Store.States(context.Background(), []unit.Unit{deck, pr})
	if err != nil || st[deck].Topic != "the deck" || st[pr].Topic != "the PR" || GoalOf(st[pr].Brief) != "the PR" || GoalOf(st[deck].Brief) != "the deck" {
		t.Fatalf("segment states %+v %v", st, err)
	}
	r.facts.post(jDM, ts(4), "", jOther, "any news on the PR?", jT0.Add(5*time.Minute))
	r.run(jT0.Add(8 * time.Minute))
	if got := r.segs.spans[1]; len(got) != 2 || got[0].ID != strings.ReplaceAll(ts(1), ".", "") || got[1].ID != strings.ReplaceAll(ts(3), ".", "") {
		t.Fatalf("second Apply %+v: an existing span must be named by its id", got)
	}
	if st, _ := r.job.Store.States(context.Background(), []unit.Unit{pr}); st[pr].Topic != "the PR, again" {
		t.Fatalf("relabel lost the segment: %+v", st)
	}
}

func TestWatchCalls(t *testing.T) {
	r := newRig(t)
	r.cfg.Items.Heat.WarmFor = config.Days(time.Hour)
	objA, objB := slack.ObjectRef{Conversation: jChan, TS: ts(1)}, slack.ObjectRef{Conversation: jChan, TS: ts(10)}
	r.facts.post(jChan, ts(1), "", jOther, "review please", jT0)
	r.facts.post(jChan, ts(10), "", jViewer, "can someone check this", jT0)
	r.asker.reply = func(_ int, q host.Ask) ([]byte, error) {
		if strings.Contains(q.Prompt, "review please") {
			return answerJSON(t, []obj{jItem("n1", ts(1))}, nil, nil, nil), nil
		}
		return answerJSON(t, []obj{jItem("n1", ts(10))}, nil, nil, obj{"started_by_viewer": true}), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	want := []watchCall{{obj: objA, reason: slack.ReasonExtraction}, {obj: objB, reason: slack.ReasonExtraction}}
	if !slices.Equal(r.watch.calls, want) {
		t.Fatalf("watch calls %+v", r.watch.calls)
	}
	r.watch.calls = nil
	r.facts.post(jChan, ts(2), ts(1), jViewer, "done", jT0.Add(3*time.Minute))
	r.facts.post(jChan, ts(11), ts(10), jOther, "checked", jT0.Add(3*time.Minute))
	idA, idB := "e"+strings.ReplaceAll(ts(1), ".", ""), "e"+strings.ReplaceAll(ts(10), ".", "")
	closure := false
	r.asker.reply = func(_ int, q host.Ask) ([]byte, error) {
		if strings.Contains(q.Prompt, "review please") {
			return answerJSON(t, []obj{jItem(idA, ts(1), resolved(ts(2)))}, nil, nil, nil), nil
		}
		return answerJSON(t, []obj{jItem(idB, ts(10), resolved(ts(11)))}, nil, nil, obj{"started_by_viewer": true, "closure_posted": closure}), nil
	}
	r.run(jT0.Add(6 * time.Minute))
	if len(r.watch.calls) != 0 {
		t.Fatalf("unwatched before warm_for: %+v", r.watch.calls)
	}
	r.run(jT0.Add(2 * time.Hour))
	if !slices.Equal(r.watch.calls, []watchCall{{obj: objA, off: true}}) {
		t.Fatalf("after warm_for: %+v; want only the unit the viewer did not start", r.watch.calls)
	}
	r.watch.calls = nil
	closure = true
	r.facts.post(jChan, ts(12), ts(10), jViewer, "closing, thanks", jT0.Add(2*time.Hour))
	r.run(jT0.Add(2*time.Hour + 2*time.Minute))
	r.run(jT0.Add(4 * time.Hour))
	if !slices.Equal(r.watch.calls, []watchCall{{obj: objB, off: true}}) {
		t.Fatalf("after the closure: %+v", r.watch.calls)
	}
	r.watch.calls = nil
	r.run(jT0.Add(5 * time.Hour))
	if len(r.watch.calls) != 0 {
		t.Fatalf("unwatched twice: %+v", r.watch.calls)
	}
}

func TestAClosureMarkUnwatchesAThreadTheViewerStartedAfterWarmFor(t *testing.T) {
	r := newRig(t)
	r.cfg.Items.Heat.WarmFor = config.Days(time.Hour)
	ref := slack.ObjectRef{Conversation: jChan, TS: ts(10)}
	r.facts.post(jChan, ts(10), "", jViewer, "can someone check this", jT0)
	r.asker.reply = func(_ int, q host.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("n1", ts(10))}, nil, nil, obj{"started_by_viewer": true}), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	r.watch.calls = nil
	r.facts.post(jChan, ts(11), ts(10), jOther, "checked", jT0.Add(3*time.Minute))
	id := "e" + strings.ReplaceAll(ts(10), ".", "")
	r.asker.reply = func(_ int, q host.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem(id, ts(10), resolved(ts(11)))}, nil, nil, obj{"started_by_viewer": true, "closure_posted": false}), nil
	}
	r.run(jT0.Add(6 * time.Minute))
	r.run(jT0.Add(2 * time.Hour))
	if len(r.watch.calls) != 0 {
		t.Fatalf("unwatched with no closure: %+v", r.watch.calls)
	}
	if err := r.job.Store.DB.MarkClosed(context.Background(), thread(ts(10)), jT0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.facts.post(jChan, ts(12), ts(10), jOther, "thanks", jT0.Add(2*time.Hour))
	r.run(jT0.Add(2*time.Hour + 2*time.Minute))
	r.run(jT0.Add(4 * time.Hour))
	if !slices.Equal(r.watch.calls, []watchCall{{obj: ref, off: true}}) {
		t.Fatalf("after unit.closed: %+v", r.watch.calls)
	}
}

func postUnits(r *rig, n int) {
	for i := range n {
		r.facts.post(jChan, ts(1+i), "", jOther, fmt.Sprintf("ask %d", i), jT0)
	}
}

func TestDailyCallCap(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.PerDay, r.cfg.Slack.Extraction.PerDayUSD = 50, 100
	postUnits(r, 200)
	_, skipped := r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 50 {
		t.Fatalf("%d calls, want 50", n)
	}
	if !strings.Contains(skipped, "50") || !strings.Contains(skipped, "calls") {
		t.Fatalf("skipped %q names no cap", skipped)
	}
	r.run(jT0.Add(3 * time.Minute))
	if n := r.asker.calls(); n != 50 {
		t.Fatalf("%d calls after the cap", n)
	}
}

func TestDailyCostCap(t *testing.T) {
	r := newRig(t)
	cost := 1.0
	r.asker.cost = &cost
	r.cfg.Slack.Extraction.PerDayUSD = 2.5
	postUnits(r, 5)
	_, skipped := r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 3 || !strings.Contains(skipped, "2.50") {
		t.Fatalf("%d calls, skipped %q; want 3 and the cost cap", n, skipped)
	}
	r.run(jT0.Add(time.Hour))
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d calls later the same day", n)
	}
	r.run(jT0.Add(25 * time.Hour))
	if n := r.asker.calls(); n != 5 {
		t.Fatalf("%d calls the next day, want 5", n)
	}
}

func TestUnknownCostCountsAsTheCallCap(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.PerCallUSD, r.cfg.Slack.Extraction.PerDayUSD = 1, 2
	postUnits(r, 4)
	r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 2 {
		t.Fatalf("%d calls, want 2", n)
	}
}

func TestBreakerOpensAndTrials(t *testing.T) {
	r := newRig(t)
	failing := true
	r.asker.reply = func(int, host.Ask) ([]byte, error) {
		if failing {
			return nil, errors.New("agent down")
		}
		return answerJSON(t, nil, nil, nil, nil), nil
	}
	postUnits(r, 6)
	r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d calls, want the breaker open after 3", n)
	}
	if w := r.job.Warnings(); len(w) != 1 || !strings.Contains(w[0], "breaker") {
		t.Fatalf("warnings %v", w)
	}
	r.run(jT0.Add(10 * time.Minute))
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d calls while open", n)
	}
	r.run(jT0.Add(18 * time.Minute))
	if n := r.asker.calls(); n != 4 {
		t.Fatalf("%d calls after the cool, want one trial", n)
	}
	r.run(jT0.Add(20 * time.Minute))
	if n := r.asker.calls(); n != 4 {
		t.Fatalf("%d calls after a failed trial", n)
	}
	failing = false
	r.run(jT0.Add(34 * time.Minute))
	if n := r.asker.calls(); n != 10 {
		t.Fatalf("%d calls after a good trial, want every unit", n)
	}
	if w := r.job.Warnings(); len(w) != 0 {
		t.Fatalf("warnings after the breaker closed %v", w)
	}
	var events []string
	for _, l := range r.logs {
		for _, e := range []string{"extract_breaker_opened", "extract_breaker_trial_failed", "extract_breaker_closed"} {
			if strings.Contains(l, e) {
				events = append(events, e)
			}
		}
	}
	if !slices.Equal(events, []string{"extract_breaker_opened", "extract_breaker_trial_failed", "extract_breaker_closed"}) {
		t.Fatalf("breaker events %v", events)
	}
}

func TestTierFromConfig(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.Tier = "balanced"
	r.cfg.Slack.Extraction.PerCallUSD = 0.25
	postUnits(r, 1)
	r.run(jT0.Add(2 * time.Minute))
	if n := r.asker.calls(); n != 1 || r.asker.asks[0].Tier != host.TierBalanced || r.asker.asks[0].MaxCostUSD != 0.25 {
		t.Fatalf("asks %+v", r.asker.asks)
	}
}

func TestHintsStored(t *testing.T) {
	r := newRig(t)
	r.cfg.Placement = []config.PlacementRule{{Match: "widget", Repo: "acme/widget"}}
	u := thread(ts(1))
	r.facts.post(jChan, ts(1), "", jOther, "fix the widget", jT0)
	r.asker.reply = func(int, host.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("n1", ts(1), func(it obj) {
			it["mode"], it["place"] = "session", obj{"context": "widget", "why": "the widget repo"}
		})}, nil, nil, nil), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	h, err := r.job.Store.Hints(context.Background(), u)
	want := Hint{Mode: "session", Context: "widget", Why: "the widget repo"}
	if err != nil || h["e"+strings.ReplaceAll(ts(1), ".", "")] != want {
		t.Fatalf("hints %+v %v", h, err)
	}
}

func TestInputCarriesLedgerSegmentsWindow(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.WindowChars = 200
	u := thread(ts(1))
	r.facts.post(jChan, ts(1), "", jOther, strings.Repeat("long context ", 20), jT0)
	r.facts.post(jChan, ts(2), ts(1), jOther, strings.Repeat("more context ", 20), jT0)
	anchor := unit.Anchor{Unit: u, Ref: jChan + "/" + ts(1)}
	if err := r.ledger.host.Apply(items.Extracted,
		items.Op{Op: items.OpCreate, Unit: u, Item: "e-old", Anchor: anchor, Fields: &items.Fields{Kind: ptr(items.Todo), Summary: ptr("old work")}},
		items.Op{Op: items.OpResolve, Unit: u, Item: "e-old", Anchor: anchor, Resolution: items.Done},
	); err != nil {
		t.Fatal(err)
	}
	r.run(jT0.Add(2 * time.Minute))
	r.facts.post(jChan, ts(3), ts(1), jOther, "new ask", jT0.Add(5*time.Minute))
	r.facts.post(jDM, ts(5), "", jOther, "dm topic", jT0.Add(5*time.Minute))
	if _, err := r.segs.Apply(context.Background(), jTeam, jDM, []slack.Span{{FromTS: ts(5)}}); err != nil {
		t.Fatal(err)
	}
	r.run(jT0.Add(8 * time.Minute))
	if n := r.asker.calls(); n != 3 {
		t.Fatalf("%d calls", n)
	}
	var thread, dm string
	for _, a := range r.asker.asks[1:] {
		if strings.Contains(a.Prompt, "dm topic") {
			dm = a.Prompt
		} else {
			thread = a.Prompt
		}
	}
	if !strings.Contains(thread, "e-old") || !strings.Contains(thread, "older messages left out") || !strings.Contains(thread, "new ask") {
		t.Errorf("thread prompt lacks the resolved item or the window cut:\n%s", thread)
	}
	if !strings.Contains(dm, "s1 ts="+ts(5)) {
		t.Errorf("dm prompt lacks its span:\n%s", dm)
	}
}

func ptr[T any](v T) *T { return &v }

func TestExtractionNeverOverwritesConfirmed(t *testing.T) {
	r := newRig(t)
	u := thread(ts(1))
	pr := unit.Unit{Provider: githubProvider, Key: "acme/widget#527"}
	id := "e" + strings.ReplaceAll(ts(1), ".", "")
	r.facts.post(jChan, ts(1), "", jOther, "review PR 527 by friday", jT0)
	r.asker.reply = func(n int, _ host.Ask) ([]byte, error) {
		if n == 0 {
			return answerJSON(t, []obj{jItem("n1", ts(1))}, nil, nil, nil), nil
		}
		return answerJSON(t, []obj{jItem(id, ts(1), func(it obj) { it["deadline"], it["kind"] = "2026-10-23", "todo" })}, nil,
			[]obj{{"item": id, "target": "github:" + pr.Key, "kind": "review", "evidence_ts": ts(2)}}, nil), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	user := unit.Anchor{Unit: u, Ref: "user:1"}
	if err := r.ledger.host.Apply(items.User, items.Op{Op: items.OpUpdate, Unit: u, Item: id, Anchor: user,
		Fields: &items.Fields{Deadline: ptr("2026-10-30")}}); err != nil {
		t.Fatal(err)
	}
	r.facts.post(jChan, ts(2), ts(1), jOther, "here is the PR github.com/acme/widget/pull/527", jT0.Add(5*time.Minute))
	r.run(jT0.Add(8 * time.Minute))
	its := r.items(u)
	if len(its) != 1 || its[0].Deadline != "2026-10-30" || its[0].Kind != items.Request || !its[0].Confirmed {
		t.Fatalf("the confirmed item changed: %+v", its)
	}
	props := r.ledger.host.Proposals()
	if len(props) != 1 || props[0].Item != id || props[0].Op != items.OpUpdate {
		t.Fatalf("proposals %+v", props)
	}
	ls := r.links(u)
	if len(ls) != 1 || ls[0].To != pr || ls[0].Kind != link.Review || ls[0].Item != id {
		t.Fatalf("the link op on a confirmed item applies: links %+v", ls)
	}
	resolveByReview(t, r, pr)
	if its := r.items(u); its[0].State != items.Resolved || its[0].Evidence.Kind != items.EvidenceUnitEvent {
		t.Fatalf("a review did not resolve the confirmed item: %+v", its[0])
	}
}

func TestExtractedReviewLinkResolvesOnReview(t *testing.T) {
	r := newRig(t)
	u := thread(ts(1))
	pr := unit.Unit{Provider: githubProvider, Key: "acme/widget#527"}
	r.facts.post(jChan, ts(1), "", jOther, "<@U000AAA> please review PR 527", jT0)
	r.asker.reply = func(int, host.Ask) ([]byte, error) {
		return answerJSON(t, []obj{jItem("n1", ts(1))}, nil,
			[]obj{{"item": "n1", "target": "github:" + pr.Key, "kind": "review", "evidence_ts": ts(1)}}, nil), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	resolveByReview(t, r, pr)
	its := r.items(u)
	if len(its) != 1 || its[0].State != items.Resolved || its[0].Resolution != items.Done {
		t.Fatalf("items %+v", its)
	}
}

func (r *rig) links(from unit.Unit) []link.Link {
	var out []link.Link
	for _, l := range r.ledger.host.Links() {
		if l.From == from {
			out = append(out, l)
		}
	}
	return out
}

// resolveByReview resolves the items of every review link to target, as
// the host's link resolver does on a review of target by the viewer: a
// derived resolve whose evidence is the unit's event.
func resolveByReview(t *testing.T, r *rig, target unit.Unit) {
	t.Helper()
	at := r.clock.Now().Add(time.Hour)
	ev := items.Evidence{Kind: items.EvidenceUnitEvent, Anchor: unit.Anchor{Unit: target, Ref: "review:1", At: at}}
	n := 0
	for _, l := range r.ledger.host.Links() {
		if l.To != target || l.Kind != link.Review || l.Item == "" {
			continue
		}
		n++
		if err := r.ledger.host.Apply(items.Derived, items.Op{Op: items.OpResolve, Unit: l.From, Item: l.Item, Anchor: ev.Anchor,
			Resolution: items.Done, Evidence: &ev}); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("%d review links to %s, want 1", n, target)
	}
}

func TestDailyCapSaysWhenItFrees(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.PerDay = 1
	postUnits(r, 3)
	at := jT0.Add(2 * time.Minute)
	_, skipped := r.run(at)
	l := at.Local()
	midnight := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, time.Local).Format("2006-01-02 15:04 MST")
	if !strings.Contains(skipped, "frees at "+midnight) || !strings.Contains(skipped, "2 units wait") {
		t.Fatalf("skipped %q; want when the cap frees (%s) and the queue", skipped, midnight)
	}
}

func TestAnswerSegmentsAreOrderedAndClosedBeforeApply(t *testing.T) {
	r := newRig(t)
	r.facts.post(jDM, ts(1), "", jOther, "can you review the deck", jT0)
	r.facts.post(jDM, ts(2), "", jViewer, "sure", jT0)
	r.asker.reply = func(_ int, _ host.Ask) ([]byte, error) {
		return answerJSON(t, nil, []obj{{"ref": "new", "from_ts": ts(1), "to_ts": "", "topic": "the deck"}}, nil, nil), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	r.facts.post(jDM, ts(3), "", jOther, "also the PR please", jT0.Add(5*time.Minute))
	r.facts.post(jDM, ts(4), "", jViewer, "on it", jT0.Add(5*time.Minute))
	// The answer lists the new topic first and leaves the deck's segment open.
	r.asker.reply = func(_ int, _ host.Ask) ([]byte, error) {
		return answerJSON(t, nil,
			[]obj{{"ref": "new", "from_ts": ts(3), "to_ts": "", "topic": "the PR"}, {"ref": "s1", "from_ts": ts(1), "to_ts": "", "topic": "the deck"}},
			nil, nil), nil
	}
	r.run(jT0.Add(8 * time.Minute))
	deck := strings.ReplaceAll(ts(1), ".", "")
	want := []slack.Span{{ID: deck, FromTS: ts(1), ToTS: ts(2)}, {FromTS: ts(3)}}
	if len(r.segs.spans) != 2 || !slices.Equal(r.segs.spans[1], want) {
		t.Fatalf("second Apply got %+v, want %+v: ascending, the deck closed at its last message", r.segs.spans, want)
	}
}

func TestARunRepairsThePersonKeysTheLedgerHolds(t *testing.T) {
	r := newRig(t)
	u := thread(ts(1))
	ctx := context.Background()
	kind, summary := items.Kind("request"), "review the deck"
	bad := func(ref string) items.Person { return items.Person{Key: "group:" + ref, Name: ref} }
	if _, err := r.ledger.host.Broker().Apply(ctx, items.Extracted, []items.Op{
		{Op: "create", Unit: u, Item: "n1", Anchor: unit.Anchor{Unit: u, Ref: jChan + "/" + ts(1), At: jT0},
			Fields: &items.Fields{Kind: &kind, Summary: &summary, Requester: ptr(bad("group:ana (U0001)")),
				WaitingOn: &[]items.Person{bad("group:group:macedo (U0002)"), bad("Plataforma Billing")}}},
		{Op: "assign", Unit: u, Item: "n1", Anchor: unit.Anchor{Unit: u, Ref: jChan + "/" + ts(1), At: jT0},
			Assignees: []items.Person{bad("macedo (U0002)")}, Basis: "explicit_mention"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.job.Store.DB.Save(ctx, store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{u: {LastTS: ts(1), At: jT0}}}); err != nil {
		t.Fatal(err)
	}
	r.run(jT0.Add(2 * time.Minute))
	its := r.items(u)
	if len(its) != 1 {
		t.Fatalf("items %+v", its)
	}
	it := its[0]
	if it.Requester.Key != "slack:U0001" || !slices.Equal(keys(it.WaitingOn), []string{"slack:U0002", "group:Plataforma Billing"}) ||
		!slices.Equal(keys(it.Assignees), []string{"slack:U0002"}) || it.Basis != "explicit_mention" {
		t.Fatalf("after the repair: requester %+v, waiting on %+v, assignees %+v (%s)", it.Requester, it.WaitingOn, it.Assignees, it.Basis)
	}
	r.run(jT0.Add(4 * time.Minute))
	if len(r.ledger.ops) != 1 || len(r.ledger.ops[0]) != 2 {
		t.Fatalf("Applies %+v, want one with the repair's update and assign, once per process", r.ledger.ops)
	}
}
