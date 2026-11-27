package facttask_test

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
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

// TestHeldSaysWhatTheFactsSayReadingOnlyGatesDraftsAndTurnsOncePerPass: whether the loop still holds a task is
// what its facts say of its open gates, its decisions (a failed delivery's approved gate among them) and its
// running turns, and a pass reads the gates, the failed drafts and the open turns once for every task.
func TestHeldSaysWhatTheFactsSayReadingOnlyGatesDraftsAndTurnsOncePerPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	chatC := unit.Unit{Provider: "chat", Key: "T1/C1/300.3"}
	done := func(id task.ID, u unit.Unit) task.Task {
		return e.task(task.Task{ID: id, State: "done", Category: task.CategoryDone, Title: string(id), Units: []unit.Unit{u}})
	}
	all := []task.Task{done("t-01", chatA), done("t-02", chatB), done("t-03", chatC),
		e.task(task.Task{ID: "t-04", State: "next", Category: task.CategoryReady, Title: "t-04"})}
	if _, err := e.db.Gates(func() time.Time { return monday }).Open(ctx, gate.Gate{Task: "t-01", Unit: task.UnitOf("t-01"), Kind: gate.KindPlan, Name: "plan", Subject: "1", Intents: []string{"approve"}}); err != nil {
		t.Fatal(err)
	}
	e.open = map[unit.Unit][]turn.Turn{chatB: {{State: turn.Started}}}
	e.decisions = decision.Map{Gates: map[gate.Kind]decision.Kind{gate.KindDraft: decision.SendDraft}}
	all = append(all, done("t-05", chatA))
	d, err := e.db.Drafts().Insert(ctx, draft.Draft{Unit: chatA, Kind: "reply", Text: "hi", State: draft.Failed, CreatedAt: monday})
	if err != nil {
		t.Fatal(err)
	}
	gs := e.db.Gates(func() time.Time { return monday })
	g, err := gs.Open(ctx, gate.Gate{Task: "t-05", Unit: task.UnitOf("t-05"), Kind: gate.KindDraft, Subject: strconv.FormatInt(int64(d.ID), 10), Intents: []string{"approve"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Resolve(ctx, g.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	want := map[task.ID]bool{"t-01": true, "t-02": true, "t-05": true}
	for _, en := range all {
		f, err := e.p.TaskFacts(ctx, en)
		if err != nil {
			t.Fatal(err)
		}
		if facts := catalog.HeldBy(f); facts != want[en.ID] {
			t.Fatalf("%s: its facts say held %v, want %v", en.ID, facts, want[en.ID])
		}
		if held, err := e.p.Held(ctx, en); err != nil || held != want[en.ID] {
			t.Errorf("%s: held %v (%v), want %v", en.ID, held, err, want[en.ID])
		}
	}
	e.reads = map[string]int{}
	pctx := coretasks.WithPass(ctx, &coretasks.Pass{Tasks: all})
	for _, en := range all {
		if held, err := e.p.Held(pctx, en); err != nil || held != want[en.ID] {
			t.Errorf("%s in a pass: held %v (%v), want %v", en.ID, held, err, want[en.ID])
		}
	}
	if !reflect.DeepEqual(e.reads, map[string]int{"gates": 2, "every open gate": 1, "drafts": 1, "open turns": 1}) {
		t.Errorf("a pass over %d tasks read %v to say which the loop holds", len(all), e.reads)
	}
}
