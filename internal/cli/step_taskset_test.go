package cli

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

const focusWorkflow = `summary = "Focus on a next action"
provider = "task"
when = 'task.state == "next"'

[[step]]
use = "task.set"
prop = "focus"
value = "on"
`

func TestTaskSetWritesAProp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, map[string]string{"workflows/my-focus/workflow.toml": focusWorkflow})
	r.put(t, "t-01", task.Next)
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)

	if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	props, err := r.db.Props(r.te.Now).PropsOf(ctx, "t-01")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := props.Get("focus"); !ok || v.Text != "on" {
		t.Fatalf("props %+v", props)
	}
}

func TestTaskSetParsesTheValueByItsType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	r.put(t, "t-01", task.Next)
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds, Props: r.db.Props(r.te.Now)})
	why := workflow.Why{Workflow: "w"}
	if _, err := s.Run(ctx, wfStep("task.set", "prop", "check_on", "value", "2026-11-02"), task.UnitOf("t-01"), why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	props, err := r.db.Props(r.te.Now).PropsOf(ctx, "t-01")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := props.Get("check_on"); !ok || !v.Time.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) || v.Text != "" {
		t.Fatalf("check_on %+v %v", v, ok)
	}
	refusal, err := s.Check(ctx, wfStep("task.set", "prop", "check_on", "value", "next week"), task.UnitOf("t-01"), why, automation.CheckOpts{})
	if err != nil || !strings.Contains(refusal, "YYYY-MM-DD") {
		t.Fatalf("a value its type does not read: %q %v", refusal, err)
	}
}

func TestTaskSetRefusesAPropWithoutTheWorkflowWriter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	r.put(t, "t-01", task.Next)
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds, Props: r.db.Props(r.te.Now)})
	why := workflow.Why{Workflow: "w"}
	for _, prop := range []string{"risk", "undeclared"} {
		st := wfStep("task.set", "prop", prop, "value", "high")
		refusal, err := s.Check(ctx, st, task.UnitOf("t-01"), why, automation.CheckOpts{})
		if err != nil || !strings.Contains(refusal, prop) {
			t.Errorf("%s: refusal %q %v", prop, refusal, err)
		}
		if _, err := s.Run(ctx, st, task.UnitOf("t-01"), why, automation.RunOpts{}); err == nil {
			t.Errorf("%s: ran", prop)
		}
	}
	props, err := r.db.Props(r.te.Now).PropsOf(ctx, "t-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(props) > 0 {
		t.Fatalf("wrote %+v", props)
	}
	if refusal, err := s.Check(ctx, wfStep("task.set", "prop", "focus", "value", "on"), task.UnitOf("t-01"), why, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Errorf("focus, which a workflow writes: %q %v", refusal, err)
	}
}
