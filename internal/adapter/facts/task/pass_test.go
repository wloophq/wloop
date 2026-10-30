package facttask_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// TestAPassReadsTheStoreOnceForEveryTask: the facts of the tasks of one pass read the sessions, the items,
// the open gates, the turn counts and the cards once each, and are the facts each task has on its own.
func TestAPassReadsTheStoreOnceForEveryTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	chatC := unit.Unit{Provider: "chat", Key: "T1/C1/300.3"}
	var all []task.Task
	for i, u := range []unit.Unit{chatA, chatB, chatC} {
		id := task.ID("t-0" + string(rune('1'+i)))
		it := e.item(u, "i"+string(id), items.Open, monday.Add(-48*time.Hour), items.Person{Key: "chat:ME"})
		all = append(all, e.task(waiting(id, u, monday.Add(-time.Hour), it)))
		if _, err := e.db.Cards().Save(ctx, card.Card{Unit: u, Title: "t", State: "thread", StartedByMe: i == 0}, monday); err != nil {
			t.Fatal(err)
		}
	}
	e.turns[task.UnitOf("t-02")] = 3
	alone := map[task.ID]workflow.Facts{}
	for _, en := range all {
		f, err := e.p.TaskFacts(ctx, en)
		if err != nil {
			t.Fatal(err)
		}
		alone[en.ID] = f
	}
	e.reads = map[string]int{}
	pctx := coretasks.WithPass(ctx, &coretasks.Pass{Tasks: all})
	for _, en := range all {
		f, err := e.p.TaskFacts(pctx, en)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f, alone[en.ID]) {
			t.Errorf("%s: the facts in a pass\n%v\ndiffer from its own\n%v", en.ID, f, alone[en.ID])
		}
	}
	for _, what := range []string{"sessions", "items", "gates", "turns", "cards"} {
		if n := e.reads[what]; n != 1 {
			t.Errorf("%s read %d times in a pass over %d tasks", what, n, len(all))
		}
	}
}

// TestPrepareStartsAPassOverEveryTask: a sweep's facts, read through the prepared context, read the store once
// for all the tasks and are the facts each task has on its own.
func TestPrepareStartsAPassOverEveryTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	var units []unit.Unit
	for i, u := range []unit.Unit{chatA, chatB} {
		id := task.ID("t-0" + string(rune('1'+i)))
		it := e.item(u, "i"+string(id), items.Open, monday.Add(-48*time.Hour), items.Person{Key: "chat:ME"})
		e.task(waiting(id, u, monday.Add(-time.Hour), it))
		units = append(units, task.UnitOf(id))
	}
	alone := map[unit.Unit]workflow.Facts{}
	for _, u := range units {
		f, err := e.p.Facts(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		alone[u] = f
	}
	pctx, err := e.p.Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.reads = map[string]int{}
	for _, u := range units {
		f, err := e.p.Facts(pctx, u)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f, alone[u]) {
			t.Errorf("%s: the facts in the prepared pass\n%v\ndiffer from its own\n%v", u, f, alone[u])
		}
	}
	for _, what := range []string{"sessions", "items", "gates", "turns", "cards"} {
		if n := e.reads[what]; n != 1 {
			t.Errorf("%s read %d times for %d tasks", what, n, len(units))
		}
	}
}
