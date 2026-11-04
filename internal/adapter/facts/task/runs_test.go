package facttask_test

import (
	"testing"
	"time"

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
			{Seq: 1, Intent: "start", Trigger: turn.ByHand, State: turn.Started},
			{Seq: 2, Intent: "chat", Trigger: turn.ByHand, State: turn.Sent},
		},
		pr:                 {{Seq: 3, Intent: "review", Trigger: turn.Automated, State: turn.Sent}, {Seq: 4, Intent: "act", Trigger: turn.ByHand, State: turn.Asked}},
		task.UnitOf("t-b"): {{Seq: 5, Intent: "start", Trigger: turn.Automated, State: turn.Asked}},
		task.UnitOf("t-c"): {{Seq: 6, Intent: "chat", Trigger: turn.ByHand, State: turn.Started}},
	}
	for id, want := range map[task.ID][2]int{"t-a": {2, 3}, "t-b": {0, 0}, "t-c": {0, 1}} {
		f := e.facts(id)
		if got := [2]any{f["task.turns_open_by_hand"], f["task.turns_running"]}; got != [2]any{want[0], want[1]} {
			t.Errorf("%s: turns_open_by_hand and turns_running %v, want %v", id, got, want)
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
