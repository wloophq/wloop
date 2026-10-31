package outbox

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// unitTasks finds the task open on each unit, and opens one, as the tasks service does for a gate.
type unitTasks struct {
	open   map[unit.Unit]task.ID
	opened int
}

func (u *unitTasks) TasksOf(_ context.Context, un unit.Unit) ([]task.Task, error) {
	if id, ok := u.open[un]; ok {
		return []task.Task{{ID: id, Category: task.CategoryInbox}}, nil
	}
	return nil, nil
}

func (u *unitTasks) Open(context.Context, coretasks.OpenRequest) (task.Task, error) {
	u.opened++
	return task.Task{ID: "t-held"}, nil
}

func nudge(on string, purpose string) Proposal {
	return Proposal{Unit: thread, Task: on, Purpose: purpose, Text: "any news?", Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "stalled", Rule: "stalled", Key: on + ":" + purpose}}
}

func TestADraftTakesTheTaskItsGateWaitsOn(t *testing.T) {
	e := newEnv()
	e.withTasks(&unitTasks{open: map[unit.Unit]task.ID{thread: "t-thread"}})
	d := e.propose(reply("closing this out"))
	if d.Task != "t-thread" || e.gate(d.ID).Task != "t-thread" {
		t.Fatalf("draft on task %q, its gate on %q; want both on t-thread", d.Task, e.gate(d.ID).Task)
	}
}

func TestEndingATaskWithdrawsItsHeldDraftsButTheKeptPurposes(t *testing.T) {
	e := newEnv()
	nudged := e.propose(nudge("t-1", "nudge"))
	closing := e.propose(nudge("t-1", "close"))
	other := e.propose(nudge("t-2", "nudge"))

	got, err := e.svc.Ended(ctx, "t-1", []string{"close"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != nudged.ID {
		t.Fatalf("withdrew %v, want draft %d", got, nudged.ID)
	}
	if d := e.get(nudged.ID); d.State != draft.Dropped || d.Reason != draft.WithdrawEnded {
		t.Errorf("the ended task's nudge is %s (%s), want dropped as %s", d.State, d.Reason, draft.WithdrawEnded)
	}
	if g := e.gate(nudged.ID); g.Status != gate.Superseded {
		t.Errorf("the withdrawn draft's gate is %s, want superseded", g.Status)
	}
	for _, id := range []draft.ID{closing.ID, other.ID} {
		if d := e.get(id); d.State != draft.Held {
			t.Errorf("draft %d (%s on %s) is %s, want held", id, d.Purpose, d.Task, d.State)
		}
	}
}

func TestAdoptGivesATasklessHeldDraftItsGatesTask(t *testing.T) {
	e := newEnv()
	open := e.tasklessHeld("nudge", "t-open")
	closed := e.tasklessHeld("close", "t-closed")
	gateless := e.tasklessHeld("ask", "")

	adopted, ended, err := e.svc.Adopt(ctx, func(_ context.Context, id task.ID) (bool, error) { return id != "t-open", nil })
	if err != nil {
		t.Fatal(err)
	}
	if adopted != 1 || ended != 1 {
		t.Fatalf("adopted %d, ended %d; want 1 and 1", adopted, ended)
	}
	if d := e.get(open.ID); d.Task != "t-open" || d.State != draft.Held {
		t.Errorf("the draft whose gate waits on an open task is %s on %q, want held on t-open", d.State, d.Task)
	}
	if d := e.get(closed.ID); d.Task != "t-closed" || d.State != draft.Dropped || d.Reason != draft.WithdrawEnded {
		t.Errorf("the draft whose gate waits on an ended task is %s (%s) on %q, want dropped as ended on t-closed", d.State, d.Reason, d.Task)
	}
	if d := e.get(gateless.ID); d.Task != "" || d.State != draft.Held {
		t.Errorf("the draft whose gate is on no task is %s on %q, want held as it was", d.State, d.Task)
	}
}

// withTasks gives the env's gates the tasks ts, which a gate on no task is placed on.
func (e *env) withTasks(ts *unitTasks) {
	e.decider = gates.New(gates.Deps{Store: e.gates, Reader: e.gates, Profile: draftProfile{}, Clock: e.clock, Publisher: publishTo{e}, Tasks: ts})
	e.svc.Gates = testGates{e.decider, e.gates}
}

// tasklessHeld stores a held draft of purpose with no task whose open gate waits on task on, as a store an earlier
// version migrated holds them.
func (e *env) tasklessHeld(purpose string, on task.ID) draft.Draft {
	d, err := e.store.Insert(ctx, draft.Draft{Unit: thread, Purpose: purpose, Text: "any news?", State: draft.Held, CreatedAt: t0, UpdatedAt: t0,
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "stalled", Rule: "stalled", Key: purpose}})
	if err != nil {
		panic(err)
	}
	g := gateOf(d)
	g.Name, g.Intents, g.Task = "draft", []string{"approve", "discard"}, on
	if _, err := e.gates.Open(ctx, g); err != nil {
		panic(err)
	}
	return d
}
