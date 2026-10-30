package task_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestActivityOnAWaitingTaskMarksItChangedAndKeepsItOutOfTheInbox(t *testing.T) {
	f := facts(func(f *task.UnitFacts) { f.LastHuman = now })
	have := []task.Task{
		{ID: "t-waiting", State: "waiting", Category: task.CategoryWaiting, Created: now.Add(-time.Hour)},
		{ID: "t-scheduled", State: "scheduled", Category: task.CategoryDeferred, Asleep: true, Created: now.Add(-time.Hour)},
		{ID: "t-next", State: "next", Category: task.CategoryReady, Created: now.Add(-time.Hour)},
		{ID: "t-seen", State: "waiting", Category: task.CategoryWaiting, Created: now.Add(-time.Hour), SeenAt: now},
	}
	cs := activity(f, have, now.Add(time.Minute))
	if len(cs) != 2 || cs[0].Task.ID != "t-waiting" || cs[1].Task.ID != "t-scheduled" {
		t.Fatalf("activity changed %+v, want the waiting and the scheduled task", cs)
	}
	for i, c := range cs {
		if !c.Task.Changed || c.Task.State != have[i].State || c.Task.Category != have[i].Category || c.Task.Asleep != have[i].Asleep {
			t.Errorf("%s: %+v, want it changed where it was", c.Task.ID, c.Task)
		}
	}
	if again := activity(f, []task.Task{cs[0].Task}, now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("activity marked a changed task again: %+v", again)
	}
}
