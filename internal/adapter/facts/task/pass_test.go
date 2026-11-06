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
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// TestAPassReadsTheStoreOnceForEveryTask: the facts of the tasks of one pass read the open turns, the items,
// the open gates, the turn counts, the cards and the wakes once each, and are the facts each task has on its own.
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
	if err := e.db.Wakes().Put(ctx, wake.Wake{Task: "t-03", Until: monday.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
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
	for _, what := range []string{"open turns", "items", "gates", "turns", "cards", "wakes"} {
		if n := e.reads[what]; n != 1 {
			t.Errorf("%s read %d times in a pass over %d tasks", what, n, len(all))
		}
	}
}

// TestPrepareStartsAPassOverEveryTask: a sweep's facts, read through the prepared context, read the store once
// for all the tasks and are the facts each task has on its own.
func TestAPassWorksOutATasksFactsOnceThoughItsProjectReadsThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	project := e.task(task.Task{ID: "p-1", State: "next", Category: task.CategoryReady, Title: "p", Props: task.Props{task.PropKind: {Text: task.KindProject}}})
	child := e.task(task.Task{ID: "t-1", State: "next", Category: task.CategoryReady, Title: "c", Parent: "p-1", Units: []unit.Unit{chatA}})
	pctx := coretasks.WithPass(ctx, &coretasks.Pass{Tasks: []task.Task{project, child}})
	for _, en := range []task.Task{project, child, project} {
		if _, err := e.p.TaskFacts(pctx, en); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.reads["repo of "+chatA.String()]; n != 1 {
		t.Errorf("the child's facts were worked out %d times in one pass", n)
	}
}

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
	err := e.p.Prepare(ctx, func(pctx context.Context) error {
		e.reads = map[string]int{}
		for _, u := range units {
			f, err := e.p.Facts(pctx, u)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(f, alone[u]) {
				t.Errorf("%s: the facts in the prepared pass\n%v\ndiffer from its own\n%v", u, f, alone[u])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, what := range []string{"open turns", "items", "gates", "turns", "cards"} {
		if n := e.reads[what]; n != 1 {
			t.Errorf("%s read %d times for %d tasks", what, n, len(units))
		}
	}
}
