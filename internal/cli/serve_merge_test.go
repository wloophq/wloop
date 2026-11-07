package cli

import (
	"context"
	"slices"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func TestTheMergeGateWordsNameTheTaskItKeepsAndTheOneItFolds(t *testing.T) {
	t.Parallel()
	l := turnBundled(t)
	text := verbGateText(func() *intentload.Loaded { return l })
	older := task.Task{ID: "t-00000000000000a1", Title: "Review acme/api#7"}
	newer := task.Task{ID: "t-00000000000000b2", Title: "Finish the export"}
	g := gate.Gate{Name: "merge", Kind: gate.KindProposal}
	got, err := text(context.Background(), g, coretasks.GateWords{Verb: "merge", Task: newer, Other: older, Keep: older, Fold: newer})
	if err != nil || got != `Merge "Finish the export" into "Review acme/api#7"? Its items, units, gates and drafts move over; Split gives it back.` {
		t.Fatalf("words %q, %v", got, err)
	}
	if got, err = text(context.Background(), g, coretasks.GateWords{Verb: "merge", Task: older, Other: newer, Keep: newer, Fold: older}); err != nil ||
		got != `Merge "Review acme/api#7" into "Finish the export"? Its items, units, gates and drafts move over; Split gives it back.` {
		t.Fatalf("the newer task kept: %q, %v", got, err)
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
