package facttask_test

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheTriageFactsReadTheAsksThePromiseAndTheExtraction(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	me, ana := items.Person{Key: "chat:ME"}, items.Person{Key: "chat:ANA"}
	old := e.item(chatA, "i1", items.Open, monday.Add(-3*time.Hour), me)
	newer := e.item(chatA, "i2", items.Open, monday.Add(-time.Hour), me)
	theirs := e.item(chatA, "i3", items.Open, monday.Add(-9*time.Hour), ana)
	done := e.item(chatA, "i4", items.Resolved, monday.Add(-20*time.Hour), me)
	promised := monday.Add(7 * time.Hour)
	mine := task.Task{ID: "t-01", State: "next", Category: task.CategoryReady, Title: "a", Units: []unit.Unit{chatA},
		Items: []task.ItemRef{{Unit: chatA, Item: old.ID}, {Unit: chatA, Item: newer.ID}, {Unit: chatA, Item: theirs.ID}, {Unit: chatA, Item: done.ID}}}
	mine.SetProp("promised", task.Value{Time: promised}, task.WriterPerson, monday)
	e.task(mine)
	e.unextracted[chatA] = monday.Add(-12 * time.Minute)
	e.task(task.Task{ID: "t-02", State: "next", Category: task.CategoryReady, Title: "b", Units: []unit.Unit{chatB}})

	f := e.facts("t-01")
	if got := f["task.ask_age"]; got != 3*time.Hour {
		t.Errorf("task.ask_age %v, want the age of the oldest open ask to me", got)
	}
	if got, _ := f["task.askers"].([]string); !slices.Equal(got, []string{"chat:U9"}) {
		t.Errorf("task.askers %v, want the requester of the asks to me, once", got)
	}
	if got, _ := f["task.promised"].(time.Time); !got.Equal(promised) {
		t.Errorf("task.promised %v, want %v", got, promised)
	}
	if got := f["task.extraction_lag"]; got != 12*time.Minute {
		t.Errorf("task.extraction_lag %v, want 12m", got)
	}

	f = e.facts("t-02")
	if f["task.ask_age"] != time.Duration(0) || len(f["task.askers"].([]string)) != 0 || !f["task.promised"].(time.Time).IsZero() ||
		f["task.extraction_lag"] != time.Duration(0) {
		t.Errorf("a task with no ask, promise or backlog: %v %v %v %v", f["task.ask_age"], f["task.askers"], f["task.promised"], f["task.extraction_lag"])
	}
}
