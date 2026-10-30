package cli

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
)

const diffWorkflow = `summary = "Ask to review the diff of a next action"
provider = "task"
when = 'task.state == "next"'

[[step]]
use = "gate.open"
name = "review-diff"
`

func TestGateOpenStepOpensAProfileGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	approved := diffWorkflow + "\n[[gates.approval.rule]]\nwhen = 'gate.kind == \"step\" && task.id == \"t-02\"'\nmode = \"run\"\n"
	r := newTaskRig(t, map[string]string{"workflows/my-diff/workflow.toml": diffWorkflow, "workflows/my-auto-diff/workflow.toml": approved})
	r.put(t, "t-01", task.State("next"))
	r.te.newGates(r.db, t.Logf)
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)
	for range 2 {
		if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
			t.Fatal(err)
		}
	}
	gs, err := r.db.Gates(r.te.Now).OfTask(ctx, "t-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 2 {
		t.Fatalf("gates %+v, want one per workflow", gs)
	}
	byWorkflow := map[string]gate.Gate{}
	for _, g := range gs {
		byWorkflow[g.Subject] = g
	}
	g := byWorkflow["my-diff"]
	if g.Name != "review-diff" || g.Kind != gate.KindStep || g.Status != gate.Open || g.Task != "t-01" || g.Unit != task.UnitOf("t-01") ||
		!slices.Equal(g.Intents, []string{"approve", "dismiss"}) {
		t.Fatalf("the gate gates.toml words: %+v", g)
	}
	if auto := byWorkflow["my-auto-diff"]; auto.Status != gate.Open {
		t.Fatalf("a rule that does not hold released the gate: %+v", auto)
	}

	r2 := newTaskRig(t, map[string]string{"workflows/my-auto-diff/workflow.toml": approved})
	if err := r2.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: "t-02", State: "next",
		Title: "Approve me", Origin: task.OriginUser, Created: testNow, Updated: testNow}}}); err != nil {
		t.Fatal(err)
	}
	r2.te.newGates(r2.db, t.Logf)
	ed2 := engineDeps{DB: r2.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r2.te.wireSubjects(&serveWiring{DB: r2.db, Cfg: r2.te.cfg, Engine: &ed2})
	if _, _, err := r2.te.newEngine(ed2).Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	gs, err = r2.db.Gates(r2.te.Now).OfTask(ctx, "t-02")
	if err != nil || len(gs) != 1 || gs[0].Status != gate.Approved || gs[0].Intent != "approve" || gs[0].By != task.ActorWorkflow ||
		!strings.Contains(gs[0].Rule, "my-auto-diff") {
		t.Fatalf("a rule of the workflow that holds releases the gate: %+v %v", gs, err)
	}

	te := newTestEnv(t, nil)
	userProfile(t, te, map[string]string{"workflows/my-diff/workflow.toml": strings.Replace(diffWorkflow, `"review-diff"`, `"review-dif"`, 1)})
	if p := te.loadedIntents().Problems.Error(); !strings.Contains(p, `step[0].name: names no gate of gates.toml; did you mean "review-diff"?`) {
		t.Fatalf("a gate.open naming no gate: %s", p)
	}
}

const afterDiffWorkflow = `summary = "Star a task once its diff is approved"
provider = "task"
when = 'trigger.event.kind == "gate_resolved" && trigger.event.data == "approve"'
key = ["trigger.event.id"]

[trigger]
on = ["gate_resolved"]

[[step]]
use = "task.verb"
verb = "focus"
`

func TestAWorkflowTriggersOnAResolvedStepGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, map[string]string{"workflows/my-diff/workflow.toml": diffWorkflow, "workflows/my-after-diff/workflow.toml": afterDiffWorkflow})
	r.put(t, "t-01", task.State("next"))
	svc := r.te.newGates(r.db, t.Logf)
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)
	if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	gs, err := r.db.Gates(r.te.Now).OfTask(ctx, "t-01")
	if err != nil || len(gs) != 1 {
		t.Fatalf("gates %+v %v", gs, err)
	}
	if e, _ := r.db.Tasks().Task(ctx, "t-01"); e.Prop("focus").Text == "on" {
		t.Fatal("starred before the gate was resolved")
	}
	if err := svc.Resolve(ctx, gs[0].ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
			t.Fatal(err)
		}
	}
	if e, _ := r.db.Tasks().Task(ctx, "t-01"); !(e.Prop("focus").Text == "on") {
		evs, _ := r.db.Events().Since(ctx, 0, []string{gate.EventResolved})
		t.Fatalf("the workflow on gate_resolved did not run; events %+v", evs)
	}
}

func TestApprovingTheDiffOfAStartedTaskClosesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	r.put(t, "t-01", task.State("waiting"))
	r.put(t, "t-02", task.State("waiting"))
	start := dturn.Book{Unit: task.UnitOf("t-01"), Turns: []dturn.Turn{{Intent: "start", ID: "s1", Trigger: dturn.ByHand, State: dturn.Done, SentAt: testNow}}}
	if _, err := sqlite.New(r.db).Save(ctx, start); err != nil {
		t.Fatal(err)
	}
	svc := r.te.newGates(r.db, t.Logf)
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)
	sweep := func() {
		t.Helper()
		if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
			t.Fatal(err)
		}
	}
	sweep()
	for _, id := range []task.ID{"t-01", "t-02"} {
		g, err := svc.Open(ctx, gate.Gate{Task: id, Unit: task.UnitOf(id), Kind: gate.KindStep, Name: "review-diff"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Resolve(ctx, g.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
			t.Fatal(err)
		}
	}
	sweep()
	sweep()
	if e, _ := r.db.Tasks().Task(ctx, "t-01"); e.Category != task.CategoryDone {
		t.Fatalf("the started task whose diff you approved: %+v", e)
	}
	if e, _ := r.db.Tasks().Task(ctx, "t-02"); e.Category == task.CategoryDone {
		t.Fatalf("a task no start ran on closed: %+v", e)
	}
}
