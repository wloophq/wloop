package cli

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestTaskSetReadsADateAfterNow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	r.put(t, "t-01", task.State("next"))
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds, Props: r.db.Props(r.te.Now)})
	why := workflow.Why{Workflow: "w"}
	if _, err := s.Run(ctx, wfStep("task.set", "prop", "check_on", "value", "+2d"), task.UnitOf("t-01"), why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	props, err := r.db.Props(r.te.Now).PropsOf(ctx, "t-01")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := props.Get("check_on"); !v.Time.Equal(r.te.Now().Add(48 * time.Hour)) {
		t.Fatalf("check_on %v, want two days after %v", v.Time, r.te.Now())
	}
	for _, bad := range []string{"+", "+soon", "+0d"} {
		if refusal, err := s.Check(ctx, wfStep("task.set", "prop", "check_on", "value", bad), task.UnitOf("t-01"), why, automation.CheckOpts{}); err != nil || !strings.Contains(refusal, "check_on") {
			t.Errorf("%q: refusal %q %v", bad, refusal, err)
		}
	}
	if refusal, _ := s.Check(ctx, wfStep("task.set", "prop", "size", "value", "+2d"), task.UnitOf("t-01"), why, automation.CheckOpts{}); refusal == "" {
		t.Error("a relative value on a property that is no date")
	}
}

func TestTaskCheckOnSetsACheckDateWhenNoSourceGivesOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	at := testNow.Add(-time.Hour)
	ana := task.Task{ID: "t-ana", State: "waiting", Category: task.CategoryWaiting, Title: "Ana sends the numbers", Origin: task.OriginUser, Created: at, Updated: at}
	ana.SetProp("owner", task.Value{Actors: []items.Person{{Key: "slack:U0ANA"}}}, task.WriterPerson, at)
	mine := task.Task{ID: "t-mine", State: "next", Category: task.CategoryReady, Title: "Write the report", Origin: task.OriginUser, Created: at, Updated: at}
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: ana}, {Op: task.OpCreate, Task: mine}}); err != nil {
		t.Fatal(err)
	}
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)
	if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	for id, set := range map[task.ID]bool{"t-ana": true, "t-mine": false} {
		props, err := r.db.Props(r.te.Now).PropsOf(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := props.Get("check_on"); v.Time.IsZero() == set {
			t.Errorf("%s: check_on %v, want set %v", id, v.Time, set)
		}
	}
}
