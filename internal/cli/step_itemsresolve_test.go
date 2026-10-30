package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var resolveThread = unit.Unit{Provider: "chat", Key: "C1/1700000000.000100"}

// resolveLedger is an items ledger over one unit that records what it applies.
type resolveLedger struct {
	items   []items.Item
	applied []items.Op
	prov    items.Provenance
}

func (l *resolveLedger) Apply(_ context.Context, prov items.Provenance, ops []items.Op) (coreitems.Result, error) {
	l.prov, l.applied = prov, append(l.applied, ops...)
	return coreitems.Result{}, nil
}

func (l *resolveLedger) Items(_ context.Context, u unit.Unit) ([]items.Item, error) {
	if u != resolveThread {
		return nil, nil
	}
	return l.items, nil
}

func (r *taskRig) derived(t *testing.T, id task.ID, refs ...string) {
	t.Helper()
	at := testNow.Add(-time.Hour)
	e := task.Task{ID: id, State: "waiting", Category: task.CategoryWaiting, Title: "Check the rollout", Origin: task.OriginDerived,
		Units: []unit.Unit{resolveThread}, Created: at, Updated: at}
	for _, ref := range refs {
		e.Items = append(e.Items, task.ItemRef{Unit: resolveThread, Item: ref})
	}
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
}

func TestItemsResolveResolvesTheOpenItemsTheTaskHoldsAsTheWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	r.derived(t, "t-01", "i1", "i2", "i3")
	l := &resolveLedger{items: []items.Item{{ID: "i1", Unit: resolveThread, State: items.Open}, {ID: "i2", Unit: resolveThread, State: items.Resolved},
		{ID: "i3", Unit: resolveThread, State: items.Open}, {ID: "i4", Unit: resolveThread, State: items.Open}}}
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds, Items: l})
	st, u, why := wfStep("items.resolve", "resolution", "answered"), task.UnitOf("t-01"), workflow.Why{Workflow: "slack-done-replied"}
	if refusal, err := s.Check(ctx, st, u, why, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Fatalf("check: %q %v", refusal, err)
	}
	if _, err := s.Run(ctx, st, u, why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if l.prov != items.Workflow || len(l.applied) != 2 {
		t.Fatalf("applied %+v as %s, want i1 and i3 as the workflow", l.applied, l.prov)
	}
	for i, want := range []string{"i1", "i3"} {
		o := l.applied[i]
		if o.Op != items.OpResolve || o.Item != want || o.Unit != resolveThread || o.Resolution != items.Answered ||
			o.Evidence == nil || o.Evidence.Kind != items.EvidenceUnitEvent || o.Anchor.Ref != "workflow:slack-done-replied" ||
			o.Anchor.Unit != resolveThread || !strings.Contains(o.Reason, "slack-done-replied") {
			t.Errorf("op %d %+v, want %s answered by the workflow", i, o, want)
		}
	}
}

func TestItemsResolveRefusesATaskWithNothingOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTaskRig(t, nil)
	r.derived(t, "t-01", "i2")
	l := &resolveLedger{items: []items.Item{{ID: "i2", Unit: resolveThread, State: items.Resolved}}}
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds, Items: l})
	refusal, err := s.Check(ctx, wfStep("items.resolve"), task.UnitOf("t-01"), workflow.Why{Workflow: "w"}, automation.CheckOpts{})
	if err != nil || refusal == "" {
		t.Fatalf("a task with no open item: refusal %q, %v", refusal, err)
	}
	if _, err := s.Check(ctx, wfStep("items.resolve"), resolveThread, workflow.Why{Workflow: "w"}, automation.CheckOpts{}); err == nil {
		t.Fatal("a unit that is not a task passes")
	}
}
