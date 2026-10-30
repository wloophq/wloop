package present

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/turn"
)

func TestAMuteUntilTheStateChangesWakesOnALinkSignal(t *testing.T) {
	ctx := context.Background()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	loaded := func() *intentload.Loaded { return l }
	f := workflow.Facts{"task.link_owes": []string{"github:ana"}, "task.link_signal.in_loop": false, "task.link_signal.ready": false}
	facts := func(context.Context, task.Task) (workflow.Facts, error) { return f, nil }
	when, err := catalog.Baseline(loaded, facts)(ctx, task.Task{ID: "t-1"}, untilStateChange)
	if err != nil || when == untilStateChange {
		t.Fatalf("the mute's when was not held to the task's values: %q %v", when, err)
	}
	holds := catalog.Holds(loaded, facts)
	w := wake.Wake{Task: "t-1", When: when}
	if held, err := holds(ctx, w, task.Task{ID: "t-1"}); err != nil || held {
		t.Fatalf("woke at once: %v %v", held, err)
	}
	f["task.link_signal.in_loop"] = true
	if held, err := holds(ctx, w, task.Task{ID: "t-1"}); err != nil || !held {
		t.Fatalf("a review workspace took the pull request and the mute holds: %v %v", held, err)
	}
}
