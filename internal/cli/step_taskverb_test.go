package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const clarifyWorkflow = `summary = "Move a captured task to Next"
provider = "task"
when = 'task.state == "inbox"'

[[step]]
use = "task.verb"
verb = "next"
`

const starWorkflow = `summary = "Star a task a workflow moved to Next"
provider = "task"
when = 'trigger.event.kind == "verb_next"'
key = ["trigger.event.id"]

[trigger]
on = ["verb_next"]
actors = ["workflow"]

[[step]]
use = "task.verb"
verb = "focus"
`

// taskRig is the entries service over a store, served as the daemon's.
type taskRig struct {
	te *testEnv
	db *sqlite.DB
}

func newTaskRig(t *testing.T, files map[string]string) *taskRig {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	if files != nil {
		userProfile(t, te, files)
		if l := te.loadedIntents(); len(l.Problems) > 0 {
			t.Fatal(l.Problems.Error())
		}
	}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), change.NewLog(change.DefaultWindow, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te.tasks = coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Authority: taskstest.BundledStates.Authority, Store: db.Tasks(), Facts: coretasks.CardFacts{Cards: db.Cards()}, Clock: clockFunc(func() time.Time { return te.Now() })})
	return &taskRig{te: te, db: db}
}

func (r *taskRig) put(t *testing.T, id task.ID, state task.State) {
	t.Helper()
	at := testNow.Add(-time.Hour)
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: task.Task{ID: id, State: state,
		Title: "Ship it", Origin: task.OriginUser, Created: at, Updated: at}}}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskVerbRecordsItsEventAndAnotherWorkflowReactsInChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, map[string]string{"workflows/my-clarify/workflow.toml": clarifyWorkflow, "workflows/my-star/workflow.toml": starWorkflow})
	r.put(t, "e-01", task.State("inbox"))
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)

	for range 3 {
		if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
			t.Fatal(err)
		}
	}
	e, err := r.db.Tasks().Task(ctx, "e-01")
	if err != nil {
		t.Fatal(err)
	}
	if e.State != task.State("next") || !(e.Prop("focus").Text == "on") {
		items, _ := eng.Ledger.Items(ctx, automation.Filter{})
		t.Fatalf("state %s, starred %v; items %+v", e.State, (e.Prop("focus").Text == "on"), items)
	}
	evs, err := r.db.Events().Since(ctx, 0, task.VerbEventKinds([]string{"next", "focus"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind, actor string
		chain       []string
	}{{"verb_next", "workflow:my-clarify", []string{"my-clarify"}}, {"verb_focus", "workflow:my-star", []string{"my-clarify", "my-star"}}}
	if len(evs) != len(want) {
		t.Fatalf("events %+v", evs)
	}
	for i, w := range want {
		if evs[i].Kind != w.kind || evs[i].Actor != w.actor || evs[i].Chain.Depth != len(w.chain) || !slices.Equal(evs[i].Chain.Workflows, w.chain) {
			t.Errorf("event %d %+v, want %s by %s in chain %v", i, evs[i], w.kind, w.actor, w.chain)
		}
	}
}

func TestTaskVerbCheckRefusesWhatTheTasksStateRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	r.put(t, "e-01", task.State("done"))
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds})
	u := task.UnitOf("e-01")
	refusal, err := s.Check(ctx, wfStep("task.verb", "verb", "done"), u, workflow.Why{Workflow: "w"}, automation.CheckOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refusal, "done on a done entry") {
		t.Fatalf("refusal %q", refusal)
	}
	if refusal, err := s.Check(ctx, wfStep("task.verb", "verb", "dismiss"), u, workflow.Why{Workflow: "w"}, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Fatalf("dismiss on a done entry: %q %v", refusal, err)
	}
	if refusal, err := s.Check(ctx, wfStep("task.verb", "verb", "reference"), u, workflow.Why{Workflow: "w"}, automation.CheckOpts{}); err != nil || !strings.Contains(refusal, "workflow may not run reference") {
		t.Fatalf("reference, which no by.workflow lets a workflow run: %q %v", refusal, err)
	}
}

func TestTaskVerbLeavesClosingADerivedTaskToItsItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	at := testNow.Add(-time.Hour)
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: "e-02", State: "waiting", Category: task.CategoryWaiting,
		Title: "Waiting on the reviewer", Origin: task.OriginDerived, Created: at, Updated: at}}}); err != nil {
		t.Fatal(err)
	}
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds})
	why := workflow.Why{Workflow: "w"}
	for _, v := range []string{"done", "dismiss"} {
		refusal, err := s.Check(ctx, wfStep("task.verb", "verb", v), task.UnitOf("e-02"), why, automation.CheckOpts{})
		if err != nil || !strings.Contains(refusal, "closes when its items resolve") {
			t.Errorf("%s on a derived entry: %q %v", v, refusal, err)
		}
	}
	if refusal, err := s.Check(ctx, wfStep("task.verb", "verb", "next"), task.UnitOf("e-02"), why, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Errorf("next on a derived entry: %q %v", refusal, err)
	}
}

func TestTaskVerbRunsOnlyVerbsThatMoveAStoredTask(t *testing.T) {
	t.Parallel()
	g := coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Authority: taskstest.BundledStates.Authority})
	for _, v := range task.Verbs {
		moves := false
		for _, s := range []task.State{"inbox", "next", "waiting", "scheduled", "someday", "reference", "done", "trash"} {
			moves = moves || g.Accepts(task.Task{State: s}, v, task.ActorWorkflow) == nil
		}
		want := slices.Contains([]task.Verb{task.VerbNext, task.VerbWaiting, task.VerbSchedule, task.VerbWake, task.VerbFocus, task.VerbDone, task.VerbDismiss}, v)
		if moves != want {
			t.Errorf("a workflow runs %s: %v, want %v", v, moves, want)
		}
	}
}
