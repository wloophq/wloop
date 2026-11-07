package cli

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/skips"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type skipSet map[string][]unit.Unit

func (s skipSet) Skipped(_ context.Context, workflow string, u unit.Unit) (bool, error) {
	return slices.Contains(s[workflow], u), nil
}

func TestSkippingAWorkflowWithdrawsItsHeldDraftOnTheUnit(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("wf", "purpose", "nudge"), "Any news?")
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-72*time.Hour)))
	r.run(draftThread)
	sk := &skips.Service{Store: r.db, Drafts: servedDrafts{r.deps}, Clock: clockFunc(func() time.Time { return r.now })}
	if err := sk.Skip(context.Background(), "wf", draftThread); err != nil {
		t.Fatal(err)
	}
	ds := r.drafts()
	if len(ds) != 1 || ds[0].State != draft.Dropped || ds[0].Reason != draft.WithdrawSkipped {
		t.Fatalf("drafts %+v, want one dropped as skipped", ds)
	}
	if ok, err := (unitKeeping{&unitSet{skips: sk}}).Skipped(context.Background(), "wf", draftThread); err != nil || !ok {
		t.Fatalf("the engine's Keep reads %v %v", ok, err)
	}
}

func TestATaskSubjectIsSkippedThroughItsUnits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	thread := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1.1"}
	tasks := map[task.ID]task.Task{"t1": {ID: "t1", Units: []unit.Unit{thread}}, "t2": {ID: "t2"}}
	set := newUnitSet(nil)
	set.skips = skipSet{"nudge": {thread}}
	set.taskOf = func(_ context.Context, id task.ID) (task.Task, error) {
		if e, ok := tasks[id]; ok {
			return e, nil
		}
		return task.Task{}, coretasks.ErrNoTask
	}
	k := unitKeeping{set}
	for _, c := range []struct {
		wf   string
		u    unit.Unit
		want bool
	}{
		{"nudge", thread, true},
		{"nudge", task.UnitOf("t1"), true},
		{"closure", task.UnitOf("t1"), false},
		{"nudge", task.UnitOf("t2"), false},
		{"nudge", task.UnitOf("gone"), false},
	} {
		if got, err := k.Skipped(ctx, c.wf, c.u); err != nil || got != c.want {
			t.Errorf("Skipped(%s, %s) = %v %v, want %v", c.wf, c.u, got, err, c.want)
		}
	}
	if got, err := (unitKeeping{newUnitSet(nil)}).Skipped(ctx, "nudge", thread); err != nil || got {
		t.Errorf("no skips: %v %v", got, err)
	}
}
