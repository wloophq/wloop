package cli

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// endedPRs are the GitHub plugin's workflow facts a pull request's task reads: whether it ended.
type endedPRs struct {
	mu    sync.Mutex
	ended map[unit.Unit]bool
}

func (f *endedPRs) List(context.Context, unit.ProviderID) ([]unit.Unit, error) { return nil, nil }

func (f *endedPRs) Facts(_ context.Context, u unit.Unit) (workflow.Facts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return workflow.Facts{"pr.ended": f.ended[u]}, nil
}

func (f *endedPRs) end(u unit.Unit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended[u] = true
}

// prTaskRig runs the bundled workflows over a real store whose units are pull requests the GitHub plugin
// reports and a Slack thread.
type prTaskRig struct {
	r     *replay
	prs   *endedPRs
	eng   *automation.Engine
	facts automation.Units
}

func newPRTaskRig(t *testing.T) *prTaskRig {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "pr.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := newReplayOn(t, db, replayStart)
	te := r.te
	prs := &endedPRs{ended: map[unit.Unit]bool{}}
	mods := []pluginModule{{ID: slackPlugin, Facts: r.slack}, {ID: unit.GitHub, Facts: prs}}
	ledger := te.itemsLedger(r.db, te.cfg, t.Logf)
	te.newTasks(r.db, te.cfg, ledger)
	me := task.Me{Keys: []string{"github:me", twMe.Key}}
	te.tasksMe.set(me)
	te.tasks.SetMe(me)
	te.Env.outbox = r.svc
	ed := engineDeps{DB: r.db, Modules: mods, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}, Log: t.Logf}
	w := &serveWiring{DB: r.db, Cfg: te.cfg, Modules: mods, Log: t.Logf, Engine: &ed}
	te.wireSubjects(w)
	te.wireDrafts(w)
	return &prTaskRig{r: r, prs: prs, eng: te.newEngine(ed), facts: ed.Subjects[task.Codec{}.Provider()]}
}

// pass sweeps until a sweep changes no task.
func (p *prTaskRig) pass(t *testing.T) {
	t.Helper()
	for range 5 {
		before := p.state(t)
		if _, _, err := p.eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
			t.Fatal(err)
		}
		if p.state(t) == before {
			return
		}
	}
	t.Fatal("the tasks did not settle in five sweeps")
}

func (p *prTaskRig) state(t *testing.T) string {
	t.Helper()
	ts, err := p.r.db.Tasks().Tasks(context.Background(), task.Query{})
	if err != nil {
		t.Fatal(err)
	}
	out := ""
	for _, e := range ts {
		out += string(e.ID) + ":" + string(e.State) + ";"
	}
	return out
}

// item stores an open item of the pull request u that the GitHub plugin derived, keyed on u, and syncs u.
func (p *prTaskRig) item(t *testing.T, u unit.Unit, id string, initial items.Initial, on items.Person) task.Task {
	t.Helper()
	it := items.Item{ID: id, Unit: u, Kind: items.Request, Summary: id + " " + u.Key, State: items.Open, Basis: items.NoBasis,
		Requester: items.Person{Key: "github:ana"}, WaitingOn: []items.Person{on}, Initial: initial, Confidence: items.High,
		Provenance: items.Derived, TaskKey: u.String(), Origin: unit.Anchor{Unit: u, Ref: "situation:x"}, CreatedAt: p.r.now}
	p.r.commit(it)
	p.r.sync(u)
	ts, err := p.r.db.Tasks().TasksOf(context.Background(), u)
	if err != nil || len(ts) != 1 {
		t.Fatalf("the tasks of %s %+v %v", u, ts, err)
	}
	return ts[0]
}

// merged resolves item id of the pull request u, as its merge does, and syncs u.
func (p *prTaskRig) merged(t *testing.T, u unit.Unit, id string) {
	t.Helper()
	got, err := p.r.db.Items().Load(context.Background(), []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range got[u] {
		if it.ID == id {
			it.State, it.Resolution = items.Resolved, items.Done
			p.r.commit(it)
		}
	}
	p.r.sync(u)
}

func (p *prTaskRig) owedBy(t *testing.T, id task.ID) string {
	t.Helper()
	f, err := p.facts.Facts(context.Background(), task.UnitOf(id))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f["task.owed_by"].(string)
	return s
}

var (
	ptMe  = items.Person{Key: "github:me", Name: "me"}
	ptPR1 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}
	ptPR2 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#2"}
)

func TestAReviewRequestLandsInNeedsMeNotTheInbox(t *testing.T) {
	t.Parallel()
	p := newPRTaskRig(t)
	review := p.item(t, ptPR1, "gh:review", items.InitialNext, ptMe)
	held := p.item(t, ptPR2, "gh:follow", "", ptMe)
	if held.Category != task.CategoryInbox {
		t.Fatalf("a follow item waiting on me proposes the inbox: %+v", held)
	}
	p.pass(t)
	for _, id := range []task.ID{review.ID, held.ID} {
		e := p.r.taskOf(id)
		if e.Category != task.CategoryReady || p.owedBy(t, id) != "me" {
			t.Errorf("%s: %s %s owed by %q, want ready and owed by me, so Needs me", id, e.State, e.Category, p.owedBy(t, id))
		}
	}
}

func TestAMergedPRClosesItsTask(t *testing.T) {
	t.Parallel()
	p := newPRTaskRig(t)
	alone := p.item(t, ptPR1, "gh:review", items.InitialNext, ptMe)
	linked := p.item(t, ptPR2, "gh:review", items.InitialNext, ptMe)
	if err := p.r.te.tasks.Attach(context.Background(), linked.ID, twThread); err != nil {
		t.Fatal(err)
	}
	p.pass(t)
	if e := p.r.taskOf(alone.ID); !e.Open() {
		t.Fatalf("an open pull request's task closed: %+v", e)
	}
	p.r.now = p.r.now.Add(time.Minute)
	for _, u := range []unit.Unit{ptPR1, ptPR2} {
		p.prs.end(u)
		p.merged(t, u, "gh:review")
	}
	p.pass(t)
	if e := p.r.taskOf(alone.ID); e.Category != task.CategoryDone {
		t.Errorf("the merged pull request's task is %s", e.Category)
	}
	if e := p.r.taskOf(linked.ID); !e.Open() {
		t.Errorf("a task its open thread still holds closed with its pull request: %s", e.Category)
	}
}
