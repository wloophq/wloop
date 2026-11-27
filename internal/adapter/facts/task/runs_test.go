package facttask_test

import (
	"context"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestOpenTurnsCountWhatWasStartedByHandAndWhatRuns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	e.task(task.Task{ID: "t-a", State: "inbox", Category: task.CategoryInbox, Title: "a", Units: []unit.Unit{pr}})
	e.task(task.Task{ID: "t-b", State: "next", Category: task.CategoryReady, Title: "b"})
	e.task(task.Task{ID: "t-c", State: "inbox", Category: task.CategoryInbox, Title: "c"})
	e.open = map[unit.Unit][]turn.Turn{
		task.UnitOf("t-a"): {
			{Seq: 1, Intent: "start", Trigger: turn.ByHand, State: turn.Started, SentAt: monday},
			{Seq: 2, Intent: "chat", Trigger: turn.ByHand, State: turn.Sent, SentAt: monday.Add(2 * time.Hour)},
		},
		pr:                 {{Seq: 3, Intent: "review", Trigger: turn.Automated, State: turn.Sent}, {Seq: 4, Intent: "act", Trigger: turn.ByHand, State: turn.Asked, SentAt: monday.Add(time.Hour)}},
		task.UnitOf("t-b"): {{Seq: 5, Intent: "start", Trigger: turn.Automated, State: turn.Asked}},
		task.UnitOf("t-c"): {{Seq: 6, Intent: "chat", Trigger: turn.ByHand, State: turn.Started}},
	}
	for id, want := range map[task.ID]struct {
		byHand, running int
		at              time.Time
	}{"t-a": {2, 3, monday.Add(time.Hour)}, "t-b": {0, 0, time.Time{}}, "t-c": {0, 1, time.Time{}}} {
		f := e.facts(id)
		if f["task.turns_open_by_hand"] != want.byHand || f["task.turns_running"] != want.running || !f["task.turn_by_hand_at"].(time.Time).Equal(want.at) {
			t.Errorf("%s: turns_open_by_hand, turns_running and turn_by_hand_at %v %v %v, want %+v", id,
				f["task.turns_open_by_hand"], f["task.turns_running"], f["task.turn_by_hand_at"], want)
		}
	}
}

func TestAProjectHasANextActionOfMineOnlyWhenAReadyChildIsOwedByMe(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	project := func(id task.ID) {
		e.task(task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "p", Props: task.Props{task.PropKind: {Text: task.KindProject}}})
	}
	project("p-theirs")
	project("p-mine")
	it := e.item(chatA, "i-ana", items.Open, monday.Add(-time.Hour), items.Person{Key: "chat:ANA"})
	e.task(task.Task{ID: "t-ana", State: "waiting", Category: task.CategoryReady, Title: "ana", Parent: "p-theirs", Units: []unit.Unit{chatA},
		Items: []task.ItemRef{{Unit: chatA, Item: it.ID}}})
	e.task(task.Task{ID: "t-mine", State: "next", Category: task.CategoryReady, Title: "mine", Parent: "p-mine"})
	for id, want := range map[task.ID]bool{"p-theirs": false, "p-mine": true} {
		f := e.facts(id)
		if f["task.children_ready"] != true || f["task.children_mine"] != want {
			t.Errorf("%s: children_ready %v, children_mine %v, want true and %v", id, f["task.children_ready"], f["task.children_mine"], want)
		}
	}
}

// TestASequentialProjectsNextIsItsFirstOpenChild holds task.sequential_next on the open child of a sequential
// project first by position, a closed child before it aside, alone and in a pass over every task.
func TestASequentialProjectsNextIsItsFirstOpenChild(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "p-seq", State: "next", Category: task.CategoryReady, Title: "p", Sequential: true, Props: task.Props{task.PropKind: {Text: task.KindProject}}})
	e.task(task.Task{ID: "p-any", State: "next", Category: task.CategoryReady, Title: "q", Props: task.Props{task.PropKind: {Text: task.KindProject}}})
	e.task(task.Task{ID: "c-done", State: "done", Category: task.CategoryDone, Title: "done first", Parent: "p-seq", Position: 0})
	e.task(task.Task{ID: "c-two", State: "next", Category: task.CategoryReady, Title: "second", Parent: "p-seq", Position: 2})
	e.task(task.Task{ID: "c-one", State: "next", Category: task.CategoryReady, Title: "first open", Parent: "p-seq", Position: 1})
	e.task(task.Task{ID: "c-any", State: "next", Category: task.CategoryReady, Title: "any", Parent: "p-any", Position: 0})
	want := map[task.ID]bool{"c-one": true, "c-two": false, "c-any": false}
	all, err := e.db.Tasks().Tasks(context.Background(), task.Query{All: true})
	if err != nil {
		t.Fatal(err)
	}
	pass := coretasks.WithPass(context.Background(), &coretasks.Pass{Tasks: all})
	for id, next := range want {
		alone := e.facts(id)
		in, err := e.p.TaskFacts(pass, taskOf(t, all, id))
		if err != nil {
			t.Fatal(err)
		}
		if alone["task.sequential_next"] != next || in["task.sequential_next"] != next {
			t.Errorf("%s: sequential_next alone %v, in a pass %v, want %v", id, alone["task.sequential_next"], in["task.sequential_next"], next)
		}
	}
}

func taskOf(t *testing.T, all []task.Task, id task.ID) task.Task {
	t.Helper()
	for _, e := range all {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no task %s", id)
	return task.Task{}
}
