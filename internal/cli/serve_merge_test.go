package cli

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/catalog"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

type titledTasks map[task.ID]task.Task

func (ts titledTasks) Task(_ context.Context, id task.ID) (task.Task, error) {
	if e, ok := ts[id]; ok {
		return e, nil
	}
	return task.Task{}, coretasks.ErrNoTask
}

func TestTheMergeGateWordsNameTheTaskTheSurvivorKeepsAndTheOneItFolds(t *testing.T) {
	t.Parallel()
	l := turnBundled(t)
	loaded := func() *intentload.Loaded { return l }
	older := task.Task{ID: "t-00000000000000a1", Title: "Review acme/api#7", Created: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
	newer := task.Task{ID: "t-00000000000000b2", Title: "Finish the export", Created: older.Created.Add(time.Hour)}
	clarified := map[task.ID]bool{}
	holds := catalog.Survives(loaded, func(_ context.Context, e task.Task) (workflow.Facts, error) {
		return workflow.Facts{"task.clarified": clarified[e.ID]}, nil
	})
	text := verbGateText(loaded, titledTasks{older.ID: older, newer.ID: newer}, holds)
	g := gate.Gate{Name: "merge", Kind: gate.KindProposal}
	got, err := text(context.Background(), g, "merge", newer, map[string]string{task.ArgTask: string(older.ID)})
	if err != nil || got != `Merge "Finish the export" into "Review acme/api#7"? Its items, units, gates and drafts move over; Split gives it back.` {
		t.Fatalf("words %q, %v", got, err)
	}
	clarified[newer.ID] = true
	if got, err = text(context.Background(), g, "merge", older, map[string]string{task.ArgTask: string(newer.ID)}); err != nil ||
		got != `Merge "Review acme/api#7" into "Finish the export"? Its items, units, gates and drafts move over; Split gives it back.` {
		t.Fatalf("the clarified task kept: %q, %v", got, err)
	}
}

func TestTheShapingVerbsAreTheMovesAndTheProjectNotTheMergeVerbs(t *testing.T) {
	t.Parallel()
	got := shapingVerbs(func() *intentload.Loaded { return turnBundled(t) })()
	for _, v := range []string{"next", "done", "waiting", "project", "acknowledge"} {
		if !slices.Contains(got, v) {
			t.Errorf("%s does not shape a task: %v", v, got)
		}
	}
	for _, v := range []string{"merge", "split", "relate", "unrelate", "seen", "approve", "focus"} {
		if slices.Contains(got, v) {
			t.Errorf("%s shapes a task: %v", v, got)
		}
	}
}
