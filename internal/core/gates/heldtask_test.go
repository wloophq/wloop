package gates_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestATaskOpenedToHoldGatesEndsWithItsLastGate(t *testing.T) {
	ctx := context.Background()
	store := taskstest.New()
	svc := coretasks.New(coretasks.Deps{Store: store, Items: prItems{}, Clock: clock{fixedNow}, States: taskstest.BundledStates})
	gs := gatestest.NewStore(fixedNow)
	g := gates.New(gates.Deps{Store: gs, Reader: gs, Profile: bundled, Tasks: svc, Clock: clock{fixedNow}})
	category := func(id task.ID) task.Category {
		t.Helper()
		e, err := store.Task(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return e.Category
	}
	open := func(u unit.Unit, subject string) gate.Gate {
		t.Helper()
		got, err := g.Open(ctx, gate.Gate{Kind: gate.KindDraft, Unit: u, Subject: subject}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	thread := unit.Unit{Provider: "slack", Key: "T1/C1/9.1"}
	first, second := open(thread, "1"), open(thread, "2")
	if first.Task == "" || second.Task != first.Task {
		t.Fatalf("the gates are on %q and %q, want one task opened for both", first.Task, second.Task)
	}
	if err := g.Resolve(ctx, first.ID, gates.Resolution{Intent: "discard", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if c := category(first.Task); c != task.CategoryInbox {
		t.Fatalf("one of its gates is still open, and the task is %s", c)
	}
	if err := g.Supersede(ctx, second.ID, task.ActorPerson); err != nil {
		t.Fatal(err)
	}
	if c := category(first.Task); c != task.CategoryInbox {
		t.Fatalf("a person's edit replaces a gate, and the task it holds is %s", c)
	}
	third := open(thread, "2")
	if err := g.Supersede(ctx, third.ID, task.ActorWorkflow); err != nil {
		t.Fatal(err)
	}
	if c := category(first.Task); c != task.CategoryDropped {
		t.Fatalf("its last gate withdrawn, the task is %s, want dropped", c)
	}

	sent := open(unit.Unit{Provider: "slack", Key: "T1/C1/9.2"}, "4")
	if err := g.Resolve(ctx, sent.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if c := category(sent.Task); c != task.CategoryDone {
		t.Fatalf("its gate approved, the task is %s, want done", c)
	}

	user := task.Task{ID: "t-00000000000000c1", State: "inbox", Category: task.CategoryInbox, Origin: task.OriginUser, Units: []unit.Unit{acme},
		Created: fixedNow(), Updated: fixedNow()}
	if err := store.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: user}}); err != nil {
		t.Fatal(err)
	}
	mine := open(acme, "5")
	if err := g.Resolve(ctx, mine.ID, gates.Resolution{Intent: "discard", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if c := category(user.ID); c != task.CategoryInbox {
		t.Fatalf("a task the user captured ended with its gate: %s", c)
	}
}
