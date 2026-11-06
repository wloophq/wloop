package workflow_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestNamesFindsAFactAsANameOrInATextOfTheReads(t *testing.T) {
	ctx := workflow.WithReads(context.Background(), "task.title", "{{ .Facts.task_candidates }} and task.candidates")
	for fact, want := range map[string]bool{"task.title": true, "task.candidates": true, "task.notes": false} {
		if got := workflow.Names(ctx, fact); got != want {
			t.Errorf("Names(%s) = %v, want %v", fact, got, want)
		}
	}
	if workflow.Names(context.Background(), "task.title") {
		t.Error("a ctx with no reads names a fact")
	}
}
