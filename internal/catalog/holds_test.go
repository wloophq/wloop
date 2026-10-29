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
