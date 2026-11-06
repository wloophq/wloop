package facttask_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func (e *env) ran(id task.ID, verb task.Verb, actor string) {
	e.t.Helper()
	ev := port.Event{Unit: task.UnitOf(id), Kind: task.VerbEventKind(verb), Actor: actor, At: monday}
	if err := e.db.Tasks().Apply(context.Background(), nil, ev); err != nil {
		e.t.Fatal(err)
	}
}

func clarified(t *testing.T, e *env, id task.ID) bool {
	t.Helper()
	return e.facts(id)["task.clarified"].(bool)
}

func TestClarifiedHoldsOnATaskAPersonMovedOrWroteOrFiled(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, id := range []task.ID{"t-01", "t-02", "t-03", "t-04", "t-05"} {
		e.task(task.Task{ID: id, State: "inbox", Category: task.CategoryInbox, Title: string(id), Units: []unit.Unit{chatA}})
	}
	e.ran("t-01", task.VerbNext, "person:cockpit")
	e.ran("t-02", task.VerbProject, "agent:tui")
	area := e.task(task.Task{ID: "t-03", State: "inbox", Category: task.CategoryInbox, Title: "t-03", Units: []unit.Unit{chatA}})
	area.SetProp("area", task.Value{Text: "web"}, task.WriterPerson, monday)
	e.task(area)
	e.ran("t-04", task.VerbNext, "workflow:task-triage")
	for id, want := range map[task.ID]bool{"t-01": true, "t-02": true, "t-03": true, "t-04": false, "t-05": false} {
		if got := clarified(t, e, id); got != want {
			t.Errorf("%s clarified %v, want %v", id, got, want)
		}
	}
	err := e.p.Prepare(context.Background(), func(ctx context.Context) error {
		for id, want := range map[task.ID]bool{"t-01": true, "t-04": false, "t-05": false} {
			f, err := e.p.Facts(ctx, task.UnitOf(id))
			if err != nil {
				return err
			}
			if f["task.clarified"] != want {
				t.Errorf("in a pass, %s clarified %v", id, f["task.clarified"])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClarifiedIgnoresMergeSplitRelateAndTheLoopsOwnProperties(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	shaped := e.task(task.Task{ID: "t-01", State: "inbox", Category: task.CategoryInbox, Title: "merged", Units: []unit.Unit{chatA}})
	for _, v := range []task.Verb{task.VerbMerge, task.VerbSplit, task.VerbRelate, task.VerbUnrelate, task.VerbSeen, task.VerbApprove} {
		e.ran("t-01", v, "person:cockpit")
	}
	shaped.SetProp("merged", task.Value{List: []string{"t-09"}}, task.WriterPerson, monday)
	shaped.SetProp("related", task.Value{List: []string{"t-08"}}, task.WriterAgent, monday)
	shaped.SetProp("due", task.Value{Time: monday}, task.WriterPlugin, monday)
	e.task(shaped)
	if clarified(t, e, "t-01") {
		t.Fatal("a merge, a split, a relation or the loop's own writes clarified the task")
	}
	e.reads = map[string]int{}
	err := e.p.Prepare(context.Background(), func(ctx context.Context) error {
		for range 3 {
			if _, err := e.p.Facts(ctx, task.UnitOf("t-01")); err != nil {
				return err
			}
		}
		_, ok := coretasks.PassOf(ctx)
		if !ok {
			t.Fatal("no pass")
		}
		return nil
	})
	if err != nil || e.reads["by hand"] > 1 {
		t.Fatalf("a pass read the verbs run by hand %d times, %v", e.reads["by hand"], err)
	}
}
