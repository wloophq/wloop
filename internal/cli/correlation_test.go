package cli

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const (
	olderTask = task.ID("t-00000000000000c1")
	newerTask = task.ID("t-00000000000000c2")
)

var (
	olderUnit = unit.Unit{Provider: unit.GitHub, Key: "acme/api#3"}
	newerUnit = unit.Unit{Provider: unit.GitHub, Key: "acme/web#9"}
)

// correlationRig is the sweep as serve wires it over a store holding two tasks, the newer just created,
// with the bundled workflows and an agent answering every classify with answer.
type correlationRig struct {
	t      *testing.T
	te     *testEnv
	db     *sqlite.DB
	eng    *automation.Engine
	steps  *stepSet
	asker  *recordingAsker
	mu     sync.Mutex
	now    time.Time
	events int
}

func newCorrelationRig(t *testing.T, answer string, requesters ...string) *correlationRig {
	t.Helper()
	r := &correlationRig{t: t, te: newTestEnv(t, &fake.Manager{}), asker: &recordingAsker{answer: answer}, now: testNow}
	r.te.Now = r.clock
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), change.NewLog(change.DefaultWindow, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r.db = db
	r.te.serveTasks(daemon.Deps{}, db, r.te.cfg, nil, t.Logf)
	r.te.newGates(db, t.Logf)
	r.te.consequences = verbDecider{r.te.Env}
	if len(requesters) == 0 {
		requesters = []string{"github:dana", "github:dana"}
	}
	r.item(olderUnit, "x1", requesters[0], "ship the billing export")
	r.item(newerUnit, "y1", requesters[1], "review the billing export change")
	ctx := context.Background()
	for i, e := range []task.Task{
		{ID: olderTask, Title: "Ship the billing export", Units: []unit.Unit{olderUnit}, Items: []task.ItemRef{{Unit: olderUnit, Item: "x1"}}},
		{ID: newerTask, Title: "Review acme/web#9", Units: []unit.Unit{newerUnit}, Items: []task.ItemRef{{Unit: newerUnit, Item: "y1"}}},
	} {
		at := testNow.Add(time.Duration(i-2) * time.Hour)
		e.State, e.Category, e.Origin, e.Created, e.Updated = "inbox", task.CategoryInbox, task.OriginDerived, at, at
		if err := db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
			t.Fatal(err)
		}
	}
	ed := engineDeps{DB: db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}, Asker: r.asker, Log: t.Logf}
	r.te.wireSubjects(&serveWiring{DB: db, Cfg: r.te.cfg, Engine: &ed})
	r.eng = r.te.newEngine(ed)
	r.steps = r.eng.Steps.(*stepSet)
	t.Cleanup(r.steps.classify.wait)
	r.settle(1)
	return r
}

func (r *correlationRig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *correlationRig) advance(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
}

// item writes item id on u as extraction does, created or updated.
func (r *correlationRig) item(u unit.Unit, id, requester, summary string) {
	r.t.Helper()
	ledger := r.te.itemsLedger(r.db, r.te.cfg, r.t.Logf)
	kind := items.Request
	who := items.Person{Key: requester, Name: strings.TrimPrefix(requester, "github:")}
	op := items.OpCreate
	if its, _ := ledger.Items(context.Background(), u); len(its) > 0 {
		op = items.OpUpdate
	}
	res, err := ledger.Apply(context.Background(), items.Extracted, []items.Op{{Op: op, Unit: u, Item: id,
		Anchor: unit.Anchor{Unit: u, Ref: "c:" + summary}, Fields: &items.Fields{Kind: &kind, Summary: &summary, Requester: &who}}})
	if err != nil || len(res.Refused) > 0 {
		r.t.Fatalf("%+v %v", res, err)
	}
}

// happen records kind on the newer task, as sync or the ledger does.
func (r *correlationRig) happen(kind string) {
	r.t.Helper()
	r.events++
	if err := sqlite.New(r.db).RecordEvents(context.Background(), port.Event{Unit: task.UnitOf(newerTask), Kind: kind,
		Key: strconv.Itoa(r.events), At: r.clock()}); err != nil {
		r.t.Fatal(err)
	}
}

// settle sweeps n times, letting each classify call finish and a deferred item come due.
func (r *correlationRig) settle(n int) {
	r.t.Helper()
	for range n {
		if _, _, err := r.eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
			r.t.Fatal(err)
		}
		r.steps.classify.wait()
		r.advance(automation.DefaultRetryBase)
	}
}

// merges are the merge gates either task holds, by status.
func (r *correlationRig) merges() map[gate.Status][]gate.Gate {
	r.t.Helper()
	out := map[gate.Status][]gate.Gate{}
	for _, id := range []task.ID{olderTask, newerTask} {
		gs, err := r.db.Gates(r.te.Now).OfTask(context.Background(), id)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, g := range gs {
			if g.Name == "merge" {
				out[g.Status] = append(out[g.Status], g)
			}
		}
	}
	return out
}

func (r *correlationRig) resolve(g gate.Gate, intent string, status gate.Status) {
	r.t.Helper()
	if err := r.te.gates.Resolve(context.Background(), g.ID, gates.Resolution{Status: status, Intent: intent, By: task.ActorPerson}); err != nil {
		r.t.Fatal(err)
	}
}

// verbDecider runs an approved proposal gate's verb as a person, as the daemon's Decide does.
type verbDecider struct{ e *Env }

func (verbDecider) Release(context.Context, gate.Gate) error { return nil }
func (verbDecider) Unblock(context.Context, gate.Gate) error { return nil }
func (verbDecider) Deliver(context.Context, gate.Gate) error { return nil }

func (d verbDecider) Decide(ctx context.Context, g gate.Gate) error {
	req, ok := coretasks.ProposedVerb(g)
	if !ok || g.Status != gate.Approved {
		return nil
	}
	req.Approved, req.Actor = g.ID, task.ActorPerson
	_, err := d.e.tasks.Do(ctx, g.Task, req)
	return err
}

// approve approves the one open merge gate.
func (r *correlationRig) approve() {
	r.t.Helper()
	open := r.merges()[gate.Open]
	if len(open) != 1 {
		r.t.Fatalf("merge gates %+v", r.merges())
	}
	r.resolve(open[0], "approve", gate.Approved)
}

func sameAs(id task.ID) string {
	return `{"same_work":true,"same_as":"` + string(id) + `","reason":"the same export"}`
}

const differentWork = `{"same_work":false,"same_as":"","reason":"different systems"}`

func TestASameWorkVerdictOpensAMergeGate(t *testing.T) {
	t.Parallel()
	r := newCorrelationRig(t, sameAs(olderTask))
	r.happen(task.EventCreated)
	r.settle(4)
	open := r.merges()[gate.Open]
	if len(open) != 1 {
		t.Fatalf("merge gates %+v, asked %d", r.merges(), len(r.asker.asked()))
	}
	g := open[0]
	req, ok := coretasks.ProposedVerb(g)
	if !ok || req.Verb != "merge" || g.Kind != gate.KindProposal || g.Task != newerTask || req.Args[task.ArgTask] != string(olderTask) {
		t.Fatalf("the gate proposes %+v %v: %+v", req, ok, g)
	}
	if len(r.asker.asked()) != 1 {
		t.Fatalf("asked %d times", len(r.asker.asked()))
	}
}

func TestAnApprovedCorrelationMergesTheTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCorrelationRig(t, sameAs(olderTask))
	r.happen(task.EventCreated)
	r.settle(4)
	r.approve()
	survivor, err := r.db.Tasks().Task(ctx, newerTask)
	if err != nil || survivor.ID != olderTask {
		t.Fatalf("the newer task reads as %s (%v), want merged into the older", survivor.ID, err)
	}
	held := false
	for _, ref := range survivor.Items {
		held = held || ref == task.ItemRef{Unit: newerUnit, Item: "y1"}
	}
	if !held {
		t.Fatalf("the survivor lacks the folded task's item: %+v", survivor.Items)
	}
}

func TestADifferentWorkVerdictOpensNothing(t *testing.T) {
	t.Parallel()
	r := newCorrelationRig(t, differentWork)
	r.happen(task.EventCreated)
	r.settle(4)
	if len(r.asker.asked()) != 1 {
		t.Fatalf("asked %d times", len(r.asker.asked()))
	}
	if gs := r.merges(); len(gs) > 0 {
		t.Fatalf("merge gates %+v", gs)
	}

	unsure := newCorrelationRig(t, `{"same_work":false,"same_as":"`+string(olderTask)+`","reason":"alike, not the same"}`)
	unsure.happen(task.EventCreated)
	unsure.settle(4)
	if gs := unsure.merges(); len(gs) > 0 || len(unsure.asker.asked()) != 1 {
		t.Fatalf("same_work false naming a candidate opened %+v", gs)
	}
}

func TestNoJudgmentRunsWithoutCandidates(t *testing.T) {
	t.Parallel()
	r := newCorrelationRig(t, sameAs(olderTask), "github:dana", "github:eli")
	r.happen(task.EventCreated)
	r.happen(task.EventLinked)
	r.settle(4)
	if n := len(r.asker.asked()); n != 0 {
		t.Fatalf("asked %d times with no candidate", n)
	}
	if spent, _ := r.db.Classifications().Spent(context.Background(), r.clock()); spent != 0 {
		t.Fatalf("spent %d", spent)
	}
}

func TestARefusedPairIsNotProposedAgainUntilOneOfThemChanges(t *testing.T) {
	t.Parallel()
	r := newCorrelationRig(t, sameAs(olderTask))
	r.happen(task.EventCreated)
	r.settle(4)
	open := r.merges()[gate.Open]
	if len(open) != 1 {
		t.Fatalf("merge gates %+v", r.merges())
	}
	r.resolve(open[0], "reject", gate.Rejected)
	r.advance(time.Minute)
	r.happen(task.EventLinked)
	r.settle(4)
	if gs := r.merges(); len(gs[gate.Open]) != 0 || len(gs[gate.Rejected]) != 1 {
		t.Fatalf("the refused pair was proposed again: %+v", gs)
	}
	if n := len(r.asker.asked()); n != 1 {
		t.Fatalf("asked %d times about a refused pair", n)
	}
	r.advance(time.Hour)
	r.item(olderUnit, "x1", "github:dana", "ship the billing export, with the new columns")
	r.happen(task.EventLinked)
	r.settle(4)
	if gs := r.merges(); len(gs[gate.Open]) != 1 {
		t.Fatalf("after the older task changed: %+v", gs)
	}
}

func TestAStoredJudgmentProposesOnItsRetryAfterBothTasksWereSeenWithTheDayCapSpent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCorrelationRig(t, sameAs(olderTask))
	r.te.cfg.Classify.PerDay = 1
	r.happen(task.EventCreated)
	if _, _, err := r.eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	r.steps.classify.wait()
	if spent, _ := r.db.Classifications().Spent(ctx, r.clock()); spent != 1 {
		t.Fatalf("spent %d, want the one call that stored the verdict", spent)
	}
	its, err := r.eng.Ledger.Items(ctx, automation.Filter{Workflow: "task-correlate"})
	if err != nil || len(its) != 1 || its[0].Decision != workflow.DecisionDeferred {
		t.Fatalf("before the retry: %+v %v", its, err)
	}
	r.advance(time.Second)
	for _, id := range []task.ID{olderTask, newerTask} {
		before, _ := r.eng.Units.Facts(ctx, task.UnitOf(id))
		if _, err := r.te.tasks.Do(ctx, id, coretasks.Request{Verb: "seen", Actor: task.ActorPerson}); err != nil {
			t.Fatal(err)
		}
		if after, _ := r.eng.Units.Facts(ctx, task.UnitOf(id)); after["task.fingerprint"] == before["task.fingerprint"] {
			t.Fatalf("seeing %s left its fingerprint %v", id, after["task.fingerprint"])
		}
	}
	r.advance(automation.DefaultRetryBase)
	r.settle(3)
	evs, err := r.db.EventsOf(ctx, []unit.Unit{task.UnitOf(newerTask)}, timeline.Filter{Kinds: []string{builtin.EventClassified}})
	if err != nil || len(evs) != 1 {
		t.Fatalf("classified events %+v %v, want the retry's", evs, err)
	}
	if gs := r.merges(); len(gs[gate.Open]) != 1 {
		t.Fatalf("merge gates %+v", gs)
	}
	if n := len(r.asker.asked()); n != 1 {
		t.Fatalf("asked %d times", n)
	}
	refused, err := r.eng.Ledger.Items(ctx, automation.Filter{Workflow: "task-correlate"})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range refused {
		if strings.Contains(it.Reason, "cap") {
			t.Fatalf("the retry that found the stored verdict was held by the cap: %+v", it)
		}
	}
}

func TestTheJudgmentRunsAtMostPerDayAndCachesByFingerprint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCorrelationRig(t, differentWork)
	r.te.cfg.Classify.PerDay = 1
	r.happen(task.EventCreated)
	r.settle(3)
	r.happen(task.EventLinked)
	r.settle(3)
	if n := len(r.asker.asked()); n != 1 {
		t.Fatalf("asked %d times for one set of fingerprints", n)
	}
	r.advance(time.Minute)
	e, err := r.db.Tasks().Task(ctx, newerTask)
	if err != nil {
		t.Fatal(err)
	}
	e.Notes, e.Updated = "now with the new columns", r.clock()
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	r.happen(task.EventLinked)
	r.settle(3)
	if n := len(r.asker.asked()); n != 1 {
		t.Fatalf("asked %d times past classify.per_day 1", n)
	}
	its, err := r.eng.Ledger.Items(ctx, automation.Filter{Workflow: "task-correlate"})
	if err != nil {
		t.Fatal(err)
	}
	capped := false
	for _, it := range its {
		capped = capped || strings.Contains(it.Reason, "daily classify cap 1 reached")
	}
	if !capped {
		t.Fatalf("no item says the cap held it: %+v", its)
	}
}

func TestTheJudgmentReadsAtMostTheCappedCandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCorrelationRig(t, differentWork)
	for i := range 4 {
		u := unit.Unit{Provider: unit.GitHub, Key: "acme/ops#" + strconv.Itoa(10+i)}
		r.item(u, "z1", "github:dana", "billing export step "+strconv.Itoa(i))
		at := testNow.Add(-3 * time.Hour)
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: task.ID("t-00000000000000d" + strconv.Itoa(i)),
			Title: "Extra " + strconv.Itoa(i), State: "inbox", Category: task.CategoryInbox, Origin: task.OriginDerived, Units: []unit.Unit{u},
			Items: []task.ItemRef{{Unit: u, Item: "z1"}}, Created: at, Updated: at}}}); err != nil {
			t.Fatal(err)
		}
	}
	r.te.cfg.Tasks.CandidateCap = 2
	r.happen(task.EventCreated)
	r.settle(3)
	prompts := r.asker.asked()
	if len(prompts) != 1 {
		t.Fatalf("asked %d times", len(prompts))
	}
	_, cands, _ := strings.Cut(prompts[0], "The candidates:")
	if n := strings.Count(cands, "\n- id: t-"); n != 2 {
		t.Fatalf("the prompt lists %d candidates, want the cap of 2:\n%s", n, prompts[0])
	}
}

func TestAMergeDoesNotRetriggerCorrelationOnTheSurvivor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCorrelationRig(t, sameAs(olderTask))
	r.happen(task.EventCreated)
	r.settle(4)
	r.approve()
	before, err := r.eng.Ledger.Items(ctx, automation.Filter{Workflow: "task-correlate"})
	if err != nil {
		t.Fatal(err)
	}
	r.settle(5)
	if n := len(r.asker.asked()); n != 1 {
		t.Fatalf("asked %d times after the merge", n)
	}
	if gs := r.merges(); len(gs[gate.Open]) != 0 {
		t.Fatalf("a merge gate opened after the merge: %+v", gs)
	}
	after, err := r.eng.Ledger.Items(ctx, automation.Filter{Workflow: "task-correlate"})
	if err != nil || len(after) > len(before) {
		t.Fatalf("correlation planned on the survivor: %+v %v", after, err)
	}
}

func TestTheJudgmentsOutputOnlyOpensAGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCorrelationRig(t, sameAs(olderTask))
	r.happen(task.EventCreated)
	r.settle(4)
	if len(r.merges()[gate.Open]) != 1 {
		t.Fatalf("merge gates %+v", r.merges())
	}
	for _, id := range []task.ID{olderTask, newerTask} {
		e, err := r.db.Tasks().Task(ctx, id)
		if err != nil || e.ID != id {
			t.Fatalf("%s reads as %s (%v): the verdict merged without the user", id, e.ID, err)
		}
	}

	stray := newCorrelationRig(t, sameAs("t-00000000000000ff"))
	other := unit.Unit{Provider: unit.GitHub, Key: "acme/ops#77"}
	stray.item(other, "o1", "github:eli", "rotate the search keys")
	if err := stray.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: "t-00000000000000ff", Title: "Rotate the search keys",
		State: "inbox", Category: task.CategoryInbox, Origin: task.OriginDerived, Units: []unit.Unit{other},
		Items: []task.ItemRef{{Unit: other, Item: "o1"}}, Created: testNow, Updated: testNow}}}); err != nil {
		t.Fatal(err)
	}
	stray.happen(task.EventCreated)
	stray.settle(4)
	if gs := stray.merges(); len(gs) > 0 || len(stray.asker.asked()) != 1 {
		t.Fatalf("an answer naming a task that is no candidate opened %+v", gs)
	}
}

func TestTheCorrelationWorkflowsTurnOffWithOneSwitch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCorrelationRig(t, sameAs(olderTask))
	off := false
	r.te.cfg.Workflows = map[string]config.WorkflowConfig{"task-correlate": {Enabled: &off}}
	r.happen(task.EventCreated)
	r.settle(4)
	if n := len(r.asker.asked()); n != 0 || len(r.merges()) != 0 {
		t.Fatalf("switched off, asked %d times, gates %+v", n, r.merges())
	}

	f, err := r.eng.Units.Facts(workflow.WithReads(ctx, "task.candidates"), task.UnitOf(newerTask))
	if err != nil {
		t.Fatal(err)
	}
	k, err := r.steps.classify.key(ctx, "task.candidates", task.UnitOf(newerTask), f)
	if err != nil || len(k.with) != 1 {
		t.Fatalf("key %+v %v", k, err)
	}
	if err := r.db.Classifications().Save(ctx, task.UnitOf(newerTask), "task-correlate", k.fp, []byte(sameAs(olderTask)), r.clock()); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.New(r.db).RecordEvents(ctx, port.Event{Unit: task.UnitOf(newerTask), Kind: builtin.EventClassified, Key: "stored",
		Data: workflow.ChainData("task-correlate", workflow.Chain{}.Then("task-correlate")), At: r.clock()}); err != nil {
		t.Fatal(err)
	}
	r.settle(3)
	if gs := r.merges(); len(gs) != 0 {
		t.Fatalf("a stored verdict of the switched-off correlation opened %+v", gs)
	}
	r.te.cfg.Workflows = nil
	if err := sqlite.New(r.db).RecordEvents(ctx, port.Event{Unit: task.UnitOf(newerTask), Kind: builtin.EventClassified, Key: "again",
		Data: workflow.ChainData("task-correlate", workflow.Chain{}.Then("task-correlate")), At: r.clock()}); err != nil {
		t.Fatal(err)
	}
	r.settle(3)
	if gs := r.merges(); len(gs[gate.Open]) != 1 {
		t.Fatalf("switched on, the stored verdict opened %+v", gs)
	}
}
