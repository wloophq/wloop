package catalog

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

func TestAViewAndABaselineReadTheFactsTheyName(t *testing.T) {
	ctx := context.Background()
	l := new(intentload.Loaded)
	*l = *bundled(t)
	env := builtin.WorkflowEnv(builtin.TaskSlot)
	when, err := expr.CompileWorkflow(`size(task.candidates) > 0`, env)
	if err != nil {
		t.Fatal(err)
	}
	l.Views.Views = append(slices.Clone(l.Views.Views), intentload.ViewDef{ID: "same-work", Over: task.OverTasks, When: when})
	var got []string
	facts := func(ctx context.Context, _ task.Task) (workflow.Facts, error) {
		got = workflow.ReadsOf(ctx)
		return workflow.Facts{"task.candidates": []string{"t-2"}, "task.link_owes": []string{}}, nil
	}
	lens := Lens{Loaded: func() *intentload.Loaded { return l }, Facts: facts, Now: func() time.Time { return time.Time{} }}
	if in, err := lens.Eval(ctx, "same-work", task.Task{ID: "t-1"}); err != nil || !in {
		t.Fatalf("eval %v %v", in, err)
	}
	if !slices.Contains(got, "task.candidates") {
		t.Fatalf("the view's facts were read with %v, want its when's reads", got)
	}
	got = nil
	if _, err := Baseline(func() *intentload.Loaded { return l }, facts)(ctx, task.Task{ID: "t-1"}, "task.link_owes"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"task.link_owes"}) {
		t.Fatalf("the baseline read the facts with %v", got)
	}
}

func TestASurvivorReadsTheFactsItNames(t *testing.T) {
	ctx := context.Background()
	l := new(intentload.Loaded)
	*l = *bundled(t)
	pred, err := expr.CompileWorkflow(`size(task.candidates) > 0`, intentload.SurvivorEnv(l.Tasks))
	if err != nil {
		t.Fatal(err)
	}
	l.Tasks.Verbs = maps.Clone(l.Tasks.Verbs)
	v := l.Tasks.Verbs["merge"]
	v.Survivor = pred
	l.Tasks.Verbs["merge"] = v
	var got []string
	facts := func(ctx context.Context, t task.Task) (workflow.Facts, error) {
		got = workflow.ReadsOf(ctx)
		if !slices.Contains(got, "task.candidates") {
			return workflow.Facts{"task.candidates": workflow.Unrequested{Fact: "task.candidates"}}, nil
		}
		if t.ID == "t-1" {
			return workflow.Facts{"task.candidates": []string{"t-2"}}, nil
		}
		return workflow.Facts{"task.candidates": []string{}}, nil
	}
	survives := Survives(func() *intentload.Loaded { return l }, facts)
	if holds, err := survives(ctx, "merge", task.Task{ID: "t-1"}); err != nil || !holds {
		t.Fatalf("survives t-1 = %v, %v; want true", holds, err)
	}
	if !slices.Contains(got, "task.candidates") {
		t.Fatalf("the survivor's facts were read with %v, want its reads", got)
	}
	if holds, err := survives(ctx, "merge", task.Task{ID: "t-2"}); err != nil || holds {
		t.Fatalf("survives t-2 = %v, %v; want false", holds, err)
	}
}

// BenchmarkLensEval places a task in each bundled view, its facts already read, as a pass does for every task.
func BenchmarkLensEval(b *testing.B) {
	l := bundled(b)
	facts := workflow.Facts{}
	lens := Lens{Loaded: func() *intentload.Loaded { return l }, Now: func() time.Time { return time.Time{} },
		Facts: func(context.Context, task.Task) (workflow.Facts, error) { return facts, nil }}
	ctx, e := context.Background(), task.Task{ID: "t-1", State: "next", Category: task.CategoryReady}
	for range b.N {
		for _, v := range l.Views.Views {
			if v.Over == task.OverTasks {
				_, _ = lens.Eval(ctx, v.ID, e)
			}
		}
	}
}
