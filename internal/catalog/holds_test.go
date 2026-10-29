package catalog

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func TestAWakesConditionHoldsOverTheTasksFacts(t *testing.T) {
	ctx := context.Background()
	l := bundled(t)
	owes := []string{"me"}
	holds := Holds(func() *intentload.Loaded { return l }, func(context.Context, task.Task) (workflow.Facts, error) {
		return workflow.Facts{"task.link_owes": owes}, nil
	})
	w := wake.Wake{Task: "t-1", When: `size(task.link_owes) != 1 || !("me" in task.link_owes)`}
	if got, err := holds(ctx, w, task.Task{ID: "t-1"}); err != nil || got {
		t.Fatalf("while who it owes stays: %v, %v", got, err)
	}
	owes = []string{"ana"}
	if got, err := holds(ctx, w, task.Task{ID: "t-1"}); err != nil || !got {
		t.Fatalf("once who it owes changed: %v, %v", got, err)
	}
	if _, err := holds(ctx, wake.Wake{Task: "t-1", When: `pr.state == "OPEN"`}, task.Task{ID: "t-1"}); err == nil {
		t.Fatal("a condition over a fact the task has not compiled")
	}
}

func TestABaselineHoldsAListFactToTheTasksValueNow(t *testing.T) {
	ctx := context.Background()
	l := bundled(t)
	loaded := func() *intentload.Loaded { return l }
	facts := func(context.Context, task.Task) (workflow.Facts, error) {
		return workflow.Facts{"task.link_owes": []string{"me", "github:ana"}}, nil
	}
	base := Baseline(loaded, facts)
	got, err := base(ctx, task.Task{ID: "t-1"}, "task.link_owes")
	if err != nil || got != `size(task.link_owes) != 2 || !("me" in task.link_owes) || !("github:ana" in task.link_owes)` {
		t.Fatalf("%q %v", got, err)
	}
	if held, err := Holds(loaded, facts)(ctx, wake.Wake{Task: "t-1", When: got}, task.Task{ID: "t-1"}); err != nil || held {
		t.Fatalf("the baseline holds at once: %v %v", held, err)
	}
	for _, when := range []string{`task.prop.priority == "high"`, "task.check_on"} {
		if got, err := base(ctx, task.Task{ID: "t-1"}, when); err != nil || got != when {
			t.Errorf("%q became %q %v", when, got, err)
		}
	}
}
