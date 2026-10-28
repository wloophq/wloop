package slack

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	cardsjob "github.com/wloophq/wloop/internal/readmodel/cards"
)

func TestSlackIdentityMatchesTheViewer(t *testing.T) {
	f := newFixture(t)
	if _, err := f.p.identity(context.Background()); err == nil {
		t.Fatal("an identity before any login")
	}
	f.login(t)
	f.people(t)
	who, err := f.p.identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if who.Key != "slack:"+me || who.Name != "Fer" || !slices.Equal(who.Aliases, []string{"email:me@example.test"}) {
		t.Fatalf("identity %+v", who)
	}
	id := items.Identity{Keys: append([]string{who.Key}, who.Aliases...)}
	assignee := items.Person{Key: "slack:" + me, Name: "Fer"}
	if !assignee.Is(id) || otherPerson.Is(id) {
		t.Fatalf("Is: viewer %v other %v", assignee.Is(id), otherPerson.Is(id))
	}
	if byEmail := (items.Person{Key: "github:fer", Aliases: []string{"email:me@example.test"}}); !byEmail.Is(id) {
		t.Fatal("the email alias does not match across plugins")
	}
}

// flow is the path a Slack unit takes to GTD: the read model's rows, the cards job, the entries service.
type flow struct {
	f     *fixture
	model *readmodel.Model
	gtd   *coretasks.Service
	units chan []unit.Unit
}

func newFlow(t *testing.T, f *fixture, clock segClock) *flow {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	who, err := f.p.identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me := task.Me{Keys: append([]string{who.Key}, who.Aliases...)}
	fl := &flow{f: f, units: make(chan []unit.Unit, 64)}
	fl.model = readmodel.New(f.log, f.db, nil, nil, nil, f.p.rows(readmodel.RowDeps{}))
	fl.gtd = coretasks.New(coretasks.Deps{Store: f.db.Entries(), Items: f.ledger(), Me: me, Clock: clock,
		Facts: coretasks.CardFacts{Cards: f.db.Cards(), Me: func() task.Me { return me }}})
	job := &cardsjob.Job{Store: f.db.Cards(), Ledger: f.ledger(), Providers: map[unit.ProviderID]cardsjob.Provider{dslack.Provider: {Card: Card}},
		Clock: clock, Log: t.Logf}
	wg.Go(func() { fl.model.Run(ctx, job.Changed) })
	wg.Go(func() { job.Run(ctx, fl.model) })
	return fl
}

// card waits for u's stored card to satisfy ok.
func (fl *flow) card(t *testing.T, u unit.Unit, ok func(card.Card) bool) card.Card {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := fl.f.db.Cards().Get(context.Background(), []unit.Unit{u})
		if err != nil {
			t.Fatal(err)
		}
		if s, have := got[u]; have && ok(s.Card) {
			return s.Card
		}
		if time.Now().After(deadline) {
			t.Fatalf("no card of %s as expected; stored %+v", u, got[u])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (fl *flow) entries(t *testing.T, u unit.Unit) []task.Entry {
	t.Helper()
	if err := fl.gtd.Sync(context.Background(), []unit.Unit{u}); err != nil {
		t.Fatal(err)
	}
	es, err := fl.f.db.Entries().EntriesOf(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func scriptedItem(ref, origin, requester string, waiting []string, state string) map[string]any {
	res, ev, kind := "none", "", "none"
	if state == "resolved" {
		res, ev, kind = "done", ts(3), "message"
	}
	return map[string]any{"ref": ref, "kind": "request", "summary": "item " + ref, "origin_ts": origin, "requester": requester,
		"assignees": waiting, "assignment_ts": origin, "assignment_basis": "explicit_mention", "waiting_on": waiting,
		"state": state, "resolution": res, "evidence_ts": ev, "evidence_kind": kind, "confidence": "high", "ambiguity": "",
		"links": []any{}, "context": "", "options": []any{}, "deadline": "", "mode": "none",
		"place": map[string]string{"context": "", "why": ""}}
}

func TestSlackItemsBecomeEntries(t *testing.T) {
	f := newFixture(t)
	clock := segClock{tsAt(4)}
	f.deps.Clock = segClock{t0}
	f.deps.Turns = &turns.Service{Items: ledgerWithLog{f.ledger()}}
	answer, err := json.Marshal(map[string]any{"brief": json.RawMessage(`{"unchanged": false, "goal": "b", "context": "", "kind": "request", "risk": "low", "risk_why": "", "open_questions": [], "tags": {"domain": [], "audience": [], "concern": []}}`), "viewer_role": "assignee", "started_by_viewer": false,
		"items": []any{
			scriptedItem("n1", ts(1), other, []string{me}, "open"),
			scriptedItem("n2", ts(2), me, []string{other}, "open"),
			scriptedItem("n3", ts(1), other, []string{me}, "resolved"),
		},
		"verdict": "waits_on_viewer", "verdict_why": "Ana waits on you", "closure_posted": false, "segments": []any{}, "links": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Agents = map[string]turns.Agent{f.cfg.Agent: askerAgent{asker: answerFunc(func(turns.Ask) ([]byte, error) { return answer, nil })}}
	f.build(t)
	f.login(t)
	f.people(t)
	f.now = t0.Add(-time.Hour)
	u := f.thread(t)
	if _, _, err := f.p.extraction(f.cfg)[0].Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	its, err := f.ledger().Items(context.Background(), u)
	if err != nil || len(its) != 3 {
		t.Fatalf("items %+v %v", its, err)
	}

	ids := map[string]string{}
	for _, it := range its {
		ids[it.Summary] = it.ID
	}
	fl := newFlow(t, f, clock)
	c := fl.card(t, u, func(c card.Card) bool { return c.State == "waits_on_viewer" })
	if len(c.WaitsOn) != 2 {
		t.Fatalf("card %+v", c)
	}
	es := fl.entries(t, u)
	var action, waiting []task.Entry
	for _, e := range es {
		switch e.Kind {
		case task.KindAction:
			action = append(action, e)
		case task.KindWaiting:
			waiting = append(waiting, e)
		default:
			t.Errorf("unexpected entry %+v", e)
		}
	}
	if len(action) != 1 || action[0].State != task.Inbox || action[0].Items[0].Item != ids["item n1"] || !slices.Contains(action[0].Units, u) {
		t.Errorf("action %+v", action)
	}
	if len(waiting) != 1 || waiting[0].State != task.Waiting || waiting[0].Waiting == nil || waiting[0].Waiting.On[0].Key != otherPerson.Key ||
		waiting[0].Items[0].Item != ids["item n2"] {
		t.Errorf("waiting %+v", waiting)
	}
}

func TestNewReplyResurfacesSnoozed(t *testing.T) {
	f := newFixture(t)
	clock := segClock{tsAt(4)}
	f.deps.Clock = clock
	f.build(t)
	f.login(t)
	f.people(t)
	u := f.thread(t)
	f.state(t, u, sqlite.ExtractionState{Verdict: "waits_on_others", VerdictWhy: "Ana owes the doc", StartedByViewer: true, At: clock.now})
	applyOps(t, f.ledger(), items.Extracted, createOp(u, "e1", 2, mePerson, otherPerson))

	fl := newFlow(t, f, clock)
	first := fl.card(t, u, func(c card.Card) bool { return c.State == "waits_on_others" })
	es := fl.entries(t, u)
	if len(es) != 1 || es[0].Kind != task.KindWaiting || es[0].State != task.Waiting {
		t.Fatalf("entries %+v", es)
	}
	if _, err := fl.gtd.Do(context.Background(), es[0].ID, coretasks.Request{Verb: task.VerbSchedule, Args: map[string]string{"on_activity": "true"}}); err != nil {
		t.Fatal(err)
	}
	if es = fl.entries(t, u); es[0].State != task.Scheduled || es[0].Changed {
		t.Fatalf("snoozed %+v", es[0])
	}

	f.seed(t, dslack.Facts{Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), LatestReply: ts(10), ReplyCount: 3, Messages: []dslack.Message{
		{TS: ts(10), ThreadTS: ts(1), User: other, Text: "here is the doc"}}}}})
	c := fl.card(t, u, func(c card.Card) bool { return c.LastHuman != nil && c.LastHuman.At.After(first.LastHuman.At) })
	if c.LastHuman.What != "here is the doc" {
		t.Fatalf("card %+v", c.LastHuman)
	}
	es = fl.entries(t, u)
	if len(es) != 1 || es[0].State != task.Inbox || !es[0].Changed {
		t.Fatalf("after the reply %+v", es)
	}
}

func TestUnconfiguredSlackServesNoIdentityNorView(t *testing.T) {
	f := newFixture(t)
	if f.mod.Identity == nil || len(readmodel.BuildCatalog(f.mod.Rows(readmodel.RowDeps{})).Views) != 1 {
		t.Fatal("a configured Slack lacks its identity or its view")
	}
	f.cfg.Slack.ClientID = ""
	f.build(t)
	if f.mod.Identity != nil {
		t.Error("an unconfigured Slack serves an identity")
	}
	if v := readmodel.BuildCatalog(f.mod.Rows(readmodel.RowDeps{})).Views; len(v) != 0 {
		t.Errorf("an unconfigured Slack serves views %+v", v)
	}
}
