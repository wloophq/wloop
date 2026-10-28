package tasks_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAttachAppendsAUnitOnceInAttachOrder(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e := r.put(t, task.Task{ID: "e-1", Kind: task.KindCapture, State: "next", Category: task.CategoryReady, Title: "ship it", Origin: task.OriginUser})
	post := unit.Unit{Provider: "slack", Key: "T1:C1:100.1"}
	for _, u := range []unit.Unit{unitU, post, unitU} {
		if err := r.svc.Attach(ctx, e.ID, u); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.task(t, e.ID).Units; !slices.Equal(got, []unit.Unit{unitU, post}) {
		t.Fatalf("units = %v, want the pull request, then the post", got)
	}
}

func TestAttachRefusesTheTaskItselfAndAMissingTask(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e := r.put(t, task.Task{ID: "e-1", Kind: task.KindCapture, State: "next", Category: task.CategoryReady, Title: "ship it", Origin: task.OriginUser})
	if err := r.svc.Attach(ctx, e.ID, task.UnitOf(e.ID)); !errors.Is(err, coretasks.ErrArgs) {
		t.Fatalf("attaching the entry to itself: err = %v, want ErrArgs", err)
	}
	if err := r.svc.Attach(ctx, e.ID, unit.Unit{}); !errors.Is(err, coretasks.ErrArgs) {
		t.Fatalf("attaching no unit: err = %v, want ErrArgs", err)
	}
	if err := r.svc.Attach(ctx, "e-gone", unitU); !errors.Is(err, coretasks.ErrNoTask) {
		t.Fatalf("attaching to a missing entry: err = %v, want ErrNoEntry", err)
	}
	if got := r.task(t, e.ID).Units; len(got) != 0 {
		t.Fatalf("units = %v, want none", got)
	}
}
