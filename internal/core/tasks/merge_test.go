package tasks_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	prUnit     = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	askThread  = unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	otherPR    = unit.Unit{Provider: unit.GitHub, Key: "acme/web#3"}
	prTaskID   = task.DerivedID(prUnit, prUnit.String())
	askTaskID  = task.DerivedID(askThread, "s:ask")
	webTaskID  = task.DerivedID(otherPR, otherPR.String())
	byPerson   = coretasks.Request{Verb: task.VerbMerge, Actor: task.ActorPerson, Source: "cockpit"}
	splitByOne = coretasks.Request{Verb: task.VerbSplit, Actor: task.ActorPerson, Source: "cockpit"}
)

// gateDefs are the gates the merge tests open, as a profile's gates.toml declares them.
type gateDefs struct{}

var mergeGateDefs = map[string]gates.Def{
	"merge": {Name: "merge", Kind: gate.KindProposal, Label: "Merge these tasks?",
		Intents: []gates.Intent{{Name: "approve", Status: gate.Approved}, {Name: "reject", Status: gate.Rejected}}},
	"done": {Name: "done", Kind: gate.KindProposal, Label: "Close it?",
		Intents: []gates.Intent{{Name: "approve", Status: gate.Approved}, {Name: "reject", Status: gate.Rejected}}},
	"plan": {Name: "plan", Kind: gate.KindPlan, Label: "Run it?",
		Intents: []gates.Intent{{Name: "approve", Status: gate.Approved}, {Name: "reject", Status: gate.Rejected}}},
}

func (gateDefs) Gate(name string) (gates.Def, bool) { d, ok := mergeGateDefs[name]; return d, ok }

func (gateDefs) FirstOf(k gate.Kind) (gates.Def, bool) {
	for _, name := range []string{"merge", "plan"} {
		if d := mergeGateDefs[name]; d.Kind == k {
			return d, true
		}
	}
	return gates.Def{}, false
}

// serviceGates are the gates service as the tasks service reads it, as the composition root wires it.
type serviceGates struct {
	s *gates.Service
	r gates.Reader
}

func (g serviceGates) Open(ctx context.Context, gt gate.Gate) (gate.Gate, error) {
	return g.s.Open(ctx, gt, nil)
}
func (g serviceGates) Get(ctx context.Context, id int64) (gate.Gate, error) { return g.r.Get(ctx, id) }
func (g serviceGates) OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	return g.r.OfTask(ctx, id)
}
func (g serviceGates) HoldsTask(ctx context.Context, id task.ID) (bool, error) {
	return g.s.HoldsTask(ctx, id)
}
func (g serviceGates) ActingFor(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	return g.s.ActingFor(ctx, id)
}

func (g serviceGates) Supersede(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := g.s.Supersede(ctx, id, task.ActorPerson); err != nil && !errors.Is(err, gates.ErrResolved) {
			return err
		}
	}
	return nil
}

// mrig is the tasks service over a real store and the gates service.
type mrig struct {
	svc   *coretasks.Service
	db    *sqlite.DB
	store coretasks.Store
	gates *gates.Service
	gr    gates.Reader
	items *fakeItems
	facts *fakeFacts
	clock *clock
	mu    sync.Mutex
	holds map[task.ID]bool
	// reading, when set, runs as a sync reads the items of a unit.
	reading func(u unit.Unit)
}

// hookedItems are the rig's items, read with its reading hook.
type hookedItems struct{ r *mrig }

func (h hookedItems) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	if h.r.reading != nil {
		h.r.reading(u)
	}
	return h.r.items.Items(ctx, u)
}

func newMergeRig(t *testing.T) *mrig {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "merge.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &mrig{db: db, store: db.Tasks(), items: &fakeItems{byUnit: map[unit.Unit][]items.Item{}, reads: map[unit.Unit]int{}},
		facts: &fakeFacts{facts: map[unit.Unit]task.UnitFacts{}, off: map[unit.ProviderID]bool{}}, clock: &clock{t0}, holds: map[task.ID]bool{}}
	gs := db.Gates(func() time.Time { return r.clock.t })
	r.gr = gs
	r.gates = gates.New(gates.Deps{Store: gs, Reader: gs, Profile: gateDefs{}, Clock: r.clock})
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: hookedItems{r}, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: taskstest.BundledStates.Authority, Gates: serviceGates{r.gates, gs},
		Survives: func(_ context.Context, verb string, e task.Task) (bool, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return verb == string(task.VerbMerge) && r.holds[e.ID], nil
		},
		GateText: func(_ context.Context, g gate.Gate, verb string, e task.Task, args map[string]string) (string, error) {
			return fmt.Sprintf("%s %s: %s with %s", g.Name, verb, e.ID, args[task.ArgTask]), nil
		}})
	for _, u := range []unit.Unit{prUnit, askThread, otherPR} {
		r.facts.facts[u] = task.UnitFacts{Unit: u, Cares: true, Carded: true}
	}
	return r
}

func prItem(id string, mod ...func(*items.Item)) items.Item {
	it := owed(prUnit, id)
	it.TaskKey = prUnit.String()
	for _, m := range mod {
		m(&it)
	}
	return it
}

func webItem() items.Item {
	it := owed(otherPR, "gh:web")
	it.TaskKey = otherPR.String()
	return it
}

func settled(it *items.Item) { it.State = "resolved"; it.UpdatedAt = t0.Add(time.Hour) }

// seed derives the pull request's task and the Slack ask's from their items, by sync, as the daemon does.
func (r *mrig) seed(t *testing.T) (pr, ask task.Task) {
	t.Helper()
	r.items.byUnit[prUnit] = []items.Item{prItem("gh:review")}
	r.items.byUnit[askThread] = []items.Item{owed(askThread, "s:ask")}
	r.sync(t, prUnit)
	r.clock.t = t0.Add(time.Minute)
	r.sync(t, askThread)
	return r.task(t, prTaskID), r.task(t, askTaskID)
}

func (r *mrig) sync(t *testing.T, us ...unit.Unit) {
	t.Helper()
	if err := r.svc.Sync(context.Background(), us); err != nil {
		t.Fatal(err)
	}
}

func (r *mrig) task(t *testing.T, id task.ID) task.Task {
	t.Helper()
	e, err := r.store.Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (r *mrig) merge(t *testing.T, on, with task.ID) task.Task {
	t.Helper()
	req := byPerson
	req.Args = map[string]string{task.ArgTask: string(with)}
	res, err := r.svc.Do(context.Background(), on, req)
	if err != nil {
		t.Fatalf("merge %s with %s: %v", on, with, err)
	}
	return res.Task
}

func (r *mrig) split(t *testing.T, on, out task.ID) error {
	t.Helper()
	req := splitByOne
	req.Args = map[string]string{task.ArgTask: string(out)}
	_, err := r.svc.Do(context.Background(), on, req)
	return err
}

func (r *mrig) stored(t *testing.T) []task.ID {
	t.Helper()
	es, err := r.store.Tasks(context.Background(), task.Query{All: true})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]task.ID, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	slices.Sort(out)
	return out
}

func (r *mrig) open(t *testing.T, g gate.Gate) gate.Gate {
	t.Helper()
	got, err := r.gates.Open(context.Background(), g, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (r *mrig) gate(t *testing.T, id int64) gate.Gate {
	t.Helper()
	g, err := r.gr.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func subject(t *testing.T, v task.Verb, args map[string]string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"verb": v, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *mrig) proposeMerge(on, with task.ID) (coretasks.Result, error) {
	return r.svc.Do(context.Background(), on, coretasks.Request{Verb: task.VerbMerge, Actor: task.ActorWorkflow, Source: "fold",
		Args: map[string]string{task.ArgTask: string(with)}})
}

func (r *mrig) mergeGates(t *testing.T) []gate.Gate {
	t.Helper()
	gs, err := r.gr.Where(context.Background(), gates.Query{Name: "merge", Statuses: []gate.Status{gate.Open, gate.Approved}})
	if err != nil {
		t.Fatal(err)
	}
	return gs
}

// approve approves g as a person and runs what it proposed, as the daemon's consequence does.
func (r *mrig) approve(t *testing.T, g gate.Gate) error {
	t.Helper()
	if err := r.gates.Resolve(context.Background(), g.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		return err
	}
	cur := r.gate(t, g.ID)
	req, ok := coretasks.ProposedVerb(cur)
	if !ok {
		t.Fatal("no proposed verb")
	}
	_, err := r.svc.Do(context.Background(), cur.Task, req)
	return err
}

func TestMergeFoldsTheOtherTaskIntoTheSurvivorInOneWrite(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	before, err := r.store.TasksSince(context.Background(), task.Query{}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.merge(t, ask.ID, pr.ID); got.ID != pr.ID {
		t.Fatalf("the older task stays: kept %s", got.ID)
	}
	after, err := r.store.TasksSince(context.Background(), task.Query{}, coretasks.Since{Epoch: before.Epoch, Rev: before.Rev})
	if err != nil || after.Rev != before.Rev+1 || !after.Full {
		t.Fatalf("rev %d after %d, full %v, %v", after.Rev, before.Rev, after.Full, err)
	}
	y := r.task(t, pr.ID)
	if !slices.Equal(y.Units, []unit.Unit{prUnit, askThread}) || len(y.Items) != 2 || !slices.Equal(y.Prop("merged").List, []string{string(ask.ID)}) {
		t.Fatalf("survivor %+v", y)
	}
	if !slices.Equal(r.stored(t), []task.ID{pr.ID}) {
		t.Fatalf("stored %v", r.stored(t))
	}
}

func TestMergeKeepsTheClarifiedTaskWhateverTheOrderOfTheRequest(t *testing.T) {
	for _, flip := range []bool{false, true} {
		r := newMergeRig(t)
		pr, ask := r.seed(t)
		r.holds[ask.ID] = true
		a, b := pr.ID, ask.ID
		if flip {
			a, b = b, a
		}
		if got := r.merge(t, a, b); got.ID != ask.ID {
			t.Fatalf("flip %v: kept %s, want the clarified %s", flip, got.ID, ask.ID)
		}
	}
}

func TestMergeKeepsTheOlderWhenNeitherOrBothWereClarified(t *testing.T) {
	for _, both := range []bool{false, true} {
		for _, flip := range []bool{false, true} {
			r := newMergeRig(t)
			pr, ask := r.seed(t)
			r.holds[pr.ID], r.holds[ask.ID] = both, both
			a, b := pr.ID, ask.ID
			if flip {
				a, b = b, a
			}
			if got := r.merge(t, a, b); got.ID != pr.ID {
				t.Fatalf("both %v flip %v: kept %s, want the older %s", both, flip, got.ID, pr.ID)
			}
		}
	}
}

func TestMergeRefusesTheSameTaskAClosedOneAndAParent(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	req := byPerson
	for name, c := range map[string]struct {
		with task.ID
		want error
	}{
		"itself":  {pr.ID, coretasks.ErrVerb},
		"no task": {"", coretasks.ErrArgs},
		"unknown": {"t-00000000000000ee", coretasks.ErrNoTask},
	} {
		req.Args = map[string]string{task.ArgTask: string(c.with)}
		if _, err := r.svc.Do(context.Background(), pr.ID, req); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	done := r.task(t, ask.ID)
	done.Become("done", taskstest.BundledStates)
	if err := r.store.Apply(context.Background(), []task.Change{{Op: task.OpUpdate, Task: done}}); err != nil {
		t.Fatal(err)
	}
	req.Args = map[string]string{task.ArgTask: string(ask.ID)}
	if _, err := r.svc.Do(context.Background(), pr.ID, req); !errors.Is(err, coretasks.ErrVerb) {
		t.Errorf("a closed other task: %v", err)
	}
	project := task.Task{ID: "t-00000000000000f1", State: "next", Category: task.CategoryReady, Title: "Ship", Origin: task.OriginUser, Created: t0, Updated: t0}
	project.SetProp(task.PropKind, task.Value{Text: task.KindProject}, task.WriterWorkflow, t0)
	if err := r.store.Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: project}}); err != nil {
		t.Fatal(err)
	}
	req.Args = map[string]string{task.ArgTask: string(project.ID)}
	if _, err := r.svc.Do(context.Background(), pr.ID, req); !errors.Is(err, coretasks.ErrVerb) {
		t.Errorf("a parent: %v", err)
	}
	if !slices.Equal(r.stored(t), sortedIDs(prTaskID, askTaskID, project.ID)) {
		t.Fatalf("a refused merge changed the tasks: %v", r.stored(t))
	}
}

func TestMergeRefusesATaskAlreadyMergedIntoTheOther(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.merge(t, ask.ID, pr.ID)
	req := byPerson
	req.Args = map[string]string{task.ArgTask: string(ask.ID)}
	if _, err := r.svc.Do(context.Background(), pr.ID, req); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("a merge of a task with one merged into it: %v", err)
	}
}

func TestMergeRecordsAVerbEventOnBothTasks(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.merge(t, ask.ID, pr.ID)
	for _, id := range []task.ID{pr.ID, ask.ID} {
		var n int
		u := task.UnitOf(id)
		if err := r.db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM events WHERE provider = ? AND unit_key = ? AND kind = ? AND actor = ?`,
			string(u.Provider), u.Key, task.VerbEventKind(task.VerbMerge), "person:cockpit").Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: %d merge events, %v", id, n, err)
		}
	}
}

func sortedIDs(ids ...task.ID) []task.ID {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// TestSyncOfEveryMovedUnitKeepsItsItemsOnTheSurvivor syncs every unit of both tasks many times after a merge,
// with new items, a reopened one and a resolved one, and checks no item is lost or held twice.
func TestSyncOfEveryMovedUnitKeepsItsItemsOnTheSurvivor(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	y := r.merge(t, pr.ID, ask.ID)
	for i := range 5 {
		r.clock.t = t0.Add(time.Duration(i+2) * time.Hour)
		r.sync(t, prUnit, askThread)
	}
	if got := r.task(t, y.ID); len(got.Items) != 2 || !got.Open() {
		t.Fatalf("after five syncs: %+v", got)
	}
	r.items.byUnit[prUnit] = []items.Item{prItem("gh:review"), prItem("gh:new")}
	r.items.byUnit[askThread] = append(r.items.byUnit[askThread], owed(askThread, "s:followup"))
	r.sync(t, prUnit, askThread)
	if !slices.Equal(r.stored(t), sortedIDs(y.ID, task.DerivedID(askThread, "s:followup"))) {
		t.Fatalf("stored %v", r.stored(t))
	}
	if got := r.task(t, y.ID); !slices.Contains(got.Items, task.ItemRef{Unit: prUnit, Item: "gh:new"}) {
		t.Fatalf("a new item of the pull request: %+v", got.Items)
	}
	r.items.byUnit[prUnit] = []items.Item{prItem("gh:review", settled), prItem("gh:new", settled)}
	r.sync(t, prUnit)
	got := r.task(t, y.ID)
	if !got.Open() || !slices.Equal(got.Items, []task.ItemRef{{Unit: askThread, Item: "s:ask"}}) || len(got.Resolved) != 2 {
		t.Fatalf("the pull request settled, the thread still asks: %+v", got)
	}
	r.clock.t = t0.Add(10 * time.Hour)
	r.items.byUnit[prUnit] = []items.Item{prItem("gh:review", func(it *items.Item) { it.UpdatedAt = r.clock.t }), prItem("gh:new", settled)}
	r.sync(t, prUnit)
	if got := r.task(t, y.ID); !slices.Contains(got.Items, task.ItemRef{Unit: prUnit, Item: "gh:review"}) || !got.Changed {
		t.Fatalf("a reopened item: %+v", got)
	}
	if n := len(r.stored(t)); n != 2 {
		t.Fatalf("stored %v", r.stored(t))
	}
}

func TestSyncNeverRecreatesTheMergedTask(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.holds[ask.ID] = true
	if y := r.merge(t, pr.ID, ask.ID); y.ID != ask.ID {
		t.Fatalf("kept %s", y.ID)
	}
	closed := owed(askThread, "s:ask")
	settled(&closed)
	r.items.byUnit[prUnit] = []items.Item{prItem("gh:review", settled)}
	r.items.byUnit[askThread] = []items.Item{closed}
	r.sync(t, prUnit, askThread)
	if got := r.task(t, ask.ID); got.Open() {
		t.Fatalf("the survivor holds nothing open: %+v", got)
	}
	r.clock.t = t0.Add(5 * time.Hour)
	r.items.byUnit[prUnit] = []items.Item{prItem("gh:another")}
	r.sync(t, prUnit)
	if !slices.Equal(r.stored(t), []task.ID{ask.ID}) {
		t.Fatalf("the merged task's id was derived again: %v", r.stored(t))
	}
	if got := r.task(t, pr.ID); got.ID != ask.ID || !got.Open() || !slices.Contains(got.Items, task.ItemRef{Unit: prUnit, Item: "gh:another"}) {
		t.Fatalf("a new ask of the pull request: %+v", got)
	}
}

// TestAMergeAndASyncOfItsUnitsDoNotInterleave starts a merge while a sync has read the pull request's tasks
// and not yet written them: the merge waits for the sync's lock, or, without it, runs in between.
func TestAMergeAndASyncOfItsUnitsDoNotInterleave(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.items.byUnit[prUnit] = []items.Item{prItem("gh:review"), prItem("gh:race")}
	waiting := make(chan struct{}, 1)
	coretasks.SetRaceHooks(r.svc, nil, func() { waiting <- struct{}{} })
	merged := make(chan error, 1)
	var once sync.Once
	r.reading = func(u unit.Unit) {
		if u != askThread {
			return
		}
		once.Do(func() {
			go func() {
				req := byPerson
				req.Args = map[string]string{task.ArgTask: string(ask.ID)}
				_, err := r.svc.Do(context.Background(), pr.ID, req)
				merged <- err
			}()
			select {
			case <-waiting:
			case err := <-merged:
				merged <- err
			}
		})
	}
	if err := r.svc.Sync(context.Background(), []unit.Unit{prUnit, askThread}); err != nil {
		t.Fatalf("the sync around the merge: %v", err)
	}
	if err := <-merged; err != nil {
		t.Fatal(err)
	}
	r.reading = nil
	r.sync(t, prUnit, askThread)
	if !slices.Equal(r.stored(t), []task.ID{pr.ID}) {
		t.Fatalf("stored %v", r.stored(t))
	}
	if got := r.task(t, pr.ID); len(got.Items) != 3 {
		t.Fatalf("items %v", got.Items)
	}
}

func TestSplitGivesTheMergedTaskBackAndSyncsItsUnits(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.merge(t, pr.ID, ask.ID)
	r.items.byUnit[askThread] = []items.Item{owed(askThread, "s:ask"), owed(askThread, "s:ask2")}
	r.sync(t, askThread)
	r.items.byUnit[askThread] = append(r.items.byUnit[askThread], owed(askThread, "s:ask3"))
	if err := r.split(t, pr.ID, ask.ID); err != nil {
		t.Fatal(err)
	}
	back := r.task(t, ask.ID)
	if back.ID != ask.ID || !slices.Equal(back.Units, ask.Units) || back.State != ask.State || back.Title != ask.Title {
		t.Fatalf("given back %+v, was %+v", back, ask)
	}
	if !slices.Contains(back.Items, task.ItemRef{Unit: askThread, Item: "s:ask"}) {
		t.Fatalf("its own item: %+v", back.Items)
	}
	rest := r.task(t, pr.ID)
	if !slices.Equal(rest.Units, pr.Units) || !slices.Equal(rest.Items, pr.Items) || rest.Prop("merged").List != nil {
		t.Fatalf("survivor %+v, was %+v", rest, pr)
	}
	for _, id := range []task.ID{task.DerivedID(askThread, "s:ask2"), task.DerivedID(askThread, "s:ask3")} {
		if !slices.Contains(r.stored(t), id) {
			t.Fatalf("the split's sync of the thread: stored %v, want %s", r.stored(t), id)
		}
	}
	if err := r.split(t, pr.ID, ask.ID); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("a second split: %v", err)
	}
}

func TestSplitRoundTripsAnUntouchedPairFieldByField(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.merge(t, ask.ID, pr.ID)
	if err := r.split(t, pr.ID, ask.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, ask.ID); !got.Same(ask) || !got.Updated.Equal(ask.Updated) {
		t.Fatalf("X\n got %+v\nwant %+v", got, ask)
	}
	got := r.task(t, pr.ID)
	got.Updated = pr.Updated
	if !got.Same(pr) {
		t.Fatalf("Y\n got %+v\nwant %+v", got, pr)
	}
}

func TestSplitUnwindsAChainFromItsOutermostMerge(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.items.byUnit[otherPR] = []items.Item{webItem()}
	r.clock.t = t0.Add(-time.Hour)
	r.sync(t, otherPR)
	web := r.task(t, webTaskID)
	r.merge(t, pr.ID, ask.ID)
	z := r.merge(t, web.ID, pr.ID)
	if z.ID != web.ID || !slices.Equal(z.Prop("merged").List, []string{string(pr.ID), string(ask.ID)}) {
		t.Fatalf("chain survivor %+v", z)
	}
	for _, id := range []task.ID{pr.ID, ask.ID} {
		if got := r.task(t, id); got.ID != web.ID {
			t.Fatalf("%s reads as %s", id, got.ID)
		}
	}
	if err := r.split(t, web.ID, ask.ID); !errors.Is(err, coretasks.ErrVerb) {
		t.Fatalf("the inner merge split first: %v", err)
	}
	r.sync(t, askThread, prUnit, otherPR)
	if !slices.Equal(r.stored(t), []task.ID{web.ID}) {
		t.Fatalf("a sync of the chain: %v", r.stored(t))
	}
	if err := r.split(t, web.ID, pr.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.split(t, pr.ID, ask.ID); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.stored(t), sortedIDs(pr.ID, ask.ID, web.ID)) {
		t.Fatalf("stored %v", r.stored(t))
	}
	for id, u := range map[task.ID]unit.Unit{pr.ID: prUnit, ask.ID: askThread, web.ID: otherPR} {
		if got := r.task(t, id); !slices.Equal(got.Units, []unit.Unit{u}) || len(got.Items) != 1 {
			t.Fatalf("%s after the unwind: %+v", id, got)
		}
	}
}

func TestAWorkflowsMergeOpensTheMergeGate(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	if _, err := r.proposeMerge(ask.ID, pr.ID); err != nil {
		t.Fatal(err)
	}
	gs := r.mergeGates(t)
	if len(gs) != 1 || gs[0].Kind != gate.KindProposal || gs[0].Task != ask.ID || gs[0].Status != gate.Open {
		t.Fatalf("gates %+v", gs)
	}
	if len(r.stored(t)) != 2 {
		t.Fatalf("a workflow merged without the gate: %v", r.stored(t))
	}
}

func TestTheMergeGateNamesBothTasksInItsWords(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	if _, err := r.proposeMerge(ask.ID, pr.ID); err != nil {
		t.Fatal(err)
	}
	if gs := r.mergeGates(t); len(gs) != 1 || gs[0].Text != fmt.Sprintf("merge merge: %s with %s", ask.ID, pr.ID) {
		t.Fatalf("gates %+v", gs)
	}
}

func TestTwoProposalsOfOnePairInEitherOrderOpenOneGate(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	for _, p := range [][2]task.ID{{ask.ID, pr.ID}, {pr.ID, ask.ID}, {ask.ID, pr.ID}} {
		if _, err := r.proposeMerge(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if gs := r.mergeGates(t); len(gs) != 1 {
		t.Fatalf("%d gates: %+v", len(gs), gs)
	}
}

func TestAnApprovedMergeGateMergesExactlyItsPair(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	if _, err := r.proposeMerge(ask.ID, pr.ID); err != nil {
		t.Fatal(err)
	}
	g := r.mergeGates(t)[0]
	if err := r.approve(t, g); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.stored(t), []task.ID{pr.ID}) {
		t.Fatalf("stored %v", r.stored(t))
	}
	forged := coretasks.Request{Verb: task.VerbMerge, Actor: task.ActorPerson, Source: "gate", Approved: g.ID,
		Args: map[string]string{task.ArgTask: string(webTaskID)}}
	if _, err := r.svc.Do(context.Background(), pr.ID, forged); !errors.Is(err, coretasks.ErrForbidden) {
		t.Fatalf("an approval reused for another pair: %v", err)
	}
}

func TestAnApprovedMergeGateWhosePairChangedRunsNothing(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.items.byUnit[otherPR] = []items.Item{webItem()}
	r.sync(t, otherPR)
	g := r.open(t, gate.Gate{Name: "merge", Unit: task.UnitOf(webTaskID), Task: webTaskID,
		Subject: subject(t, task.VerbMerge, map[string]string{task.ArgTask: string(ask.ID)})})
	sqlitetest.Exec(t, r.db, `INSERT INTO task_aliases (old_id, new_id) VALUES (?, ?)`, string(ask.ID), string(pr.ID))
	sqlitetest.Exec(t, r.db, `DELETE FROM tasks WHERE id = ?`, string(ask.ID))
	if err := r.approve(t, g); !errors.Is(err, coretasks.ErrForbidden) {
		t.Fatalf("a gate whose other task now reads as another: %v", err)
	}
	if !slices.Equal(r.stored(t), sortedIDs(pr.ID, webTaskID)) {
		t.Fatalf("stored %v", r.stored(t))
	}
}

func TestMergeSupersedesTheMergedTasksOwnProposalsAndPlans(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.items.byUnit[otherPR] = []items.Item{webItem()}
	r.sync(t, otherPR)
	done := r.open(t, gate.Gate{Name: "done", Unit: task.UnitOf(ask.ID), Task: ask.ID, Subject: subject(t, task.VerbDone, nil)})
	plan := r.open(t, gate.Gate{Name: "plan", Unit: task.UnitOf(ask.ID), Task: ask.ID, Subject: "plan:1"})
	naming := r.open(t, gate.Gate{Name: "merge", Unit: task.UnitOf(webTaskID), Task: webTaskID,
		Subject: subject(t, task.VerbMerge, map[string]string{task.ArgTask: string(ask.ID)})})
	onThread := r.open(t, gate.Gate{Name: "plan", Unit: askThread, Task: ask.ID, Subject: "plan:2"})
	r.merge(t, ask.ID, pr.ID)
	for _, g := range []gate.Gate{done, plan, naming} {
		if got := r.gate(t, g.ID); got.Status != gate.Superseded {
			t.Errorf("gate %s %d: %s", g.Name, g.ID, got.Status)
		}
	}
	if got := r.gate(t, onThread.ID); got.Status != gate.Open || got.Task != pr.ID {
		t.Errorf("a gate of the merged task's other unit: %+v", got)
	}
	rec, ok, err := r.store.(coretasks.Merger).MergeOf(context.Background(), ask.ID)
	if err != nil || !ok || !slices.Equal(rec.Superseded, []int64{done.ID, plan.ID, naming.ID}) {
		t.Fatalf("record superseded %v, %v", rec.Superseded, err)
	}
	if err := r.approve(t, naming); err == nil {
		t.Fatal("a superseded gate approved")
	}
	if !slices.Equal(r.stored(t), sortedIDs(pr.ID, webTaskID)) {
		t.Fatalf("stored %v", r.stored(t))
	}
}

func TestSplitSupersedesTheProposalsNamingBothTasks(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.merge(t, ask.ID, pr.ID)
	again := r.open(t, gate.Gate{Name: "merge", Unit: task.UnitOf(pr.ID), Task: pr.ID,
		Subject: subject(t, task.VerbMerge, map[string]string{task.ArgTask: string(ask.ID)})})
	other := r.open(t, gate.Gate{Name: "merge", Unit: task.UnitOf(pr.ID), Task: pr.ID,
		Subject: subject(t, task.VerbMerge, map[string]string{task.ArgTask: string(webTaskID)})})
	if err := r.split(t, pr.ID, ask.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.gate(t, again.ID); got.Status != gate.Superseded {
		t.Errorf("a proposal of the pair: %s", got.Status)
	}
	if got := r.gate(t, other.ID); got.Status != gate.Open {
		t.Errorf("a proposal of another pair: %s", got.Status)
	}
}

func TestAWorkflowCannotSplit(t *testing.T) {
	r := newMergeRig(t)
	pr, ask := r.seed(t)
	r.merge(t, ask.ID, pr.ID)
	for _, a := range []task.Actor{task.ActorWorkflow, task.ActorPlugin} {
		_, err := r.svc.Do(context.Background(), pr.ID, coretasks.Request{Verb: task.VerbSplit, Actor: a, Source: "fold",
			Args: map[string]string{task.ArgTask: string(ask.ID)}})
		if !errors.Is(err, coretasks.ErrForbidden) {
			t.Fatalf("%s split: %v", a, err)
		}
	}
	_, err := r.svc.Do(context.Background(), pr.ID, coretasks.Request{Verb: task.VerbMerge, Actor: task.ActorPlugin, Source: "x",
		Args: map[string]string{task.ArgTask: string(webTaskID)}})
	if !errors.Is(err, coretasks.ErrForbidden) {
		t.Fatalf("a plugin's merge: %v", err)
	}
	if !slices.Equal(r.stored(t), []task.ID{pr.ID}) {
		t.Fatalf("stored %v", r.stored(t))
	}
}
