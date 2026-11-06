package facttask_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
)

func gatesResolution(s gate.Status) gates.Resolution {
	intent := "approve"
	if s == gate.Rejected {
		intent = "reject"
	}
	return gates.Resolution{Status: s, Intent: intent, By: task.ActorPerson}
}

func TestRefusedMergesListsARejectedPairUntilEitherTaskChanges(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	held := map[task.ID]items.Item{}
	for _, id := range []task.ID{"t-a", "t-b", "t-c", "t-d", "t-e"} {
		u := chat("T1/C1/" + string(id))
		held[id] = c.ask(u, "i1", uma, meP)
		c.open(id, u, monday.Add(-time.Hour), held[id])
	}
	c.reject("t-a", "t-b", task.VerbMerge)
	c.reject("t-e", "t-a", task.VerbMerge)
	c.resolve("t-a", "t-c", task.VerbMerge, gate.Approved)
	c.reject("t-a", "t-d", task.VerbRelate)
	refused := func(id task.ID) []string { return c.list(context.Background(), id, "task.refused_merges") }
	if got, want := refused("t-a"), []string{"t-b", "t-e"}; !slices.Equal(got, want) {
		t.Fatalf("refused merges of t-a %v, want %v", got, want)
	}
	if got, want := refused("t-b"), []string{"t-a"}; !slices.Equal(got, want) {
		t.Fatalf("refused merges of t-b %v, want %v", got, want)
	}
	c.clock.now = monday.Add(time.Hour)
	b, err := c.db.Tasks().Task(context.Background(), "t-b")
	if err != nil {
		t.Fatal(err)
	}
	b.Changed, b.SeenAt, b.Updated = false, c.clock.now, c.clock.now
	c.task(b)
	if got, want := refused("t-b"), []string{"t-a"}; !slices.Equal(got, want) {
		t.Fatalf("refused merges of t-b once seen %v, want %v: seeing a task changes none of its items", got, want)
	}
	it := held["t-b"]
	it.Summary, it.UpdatedAt = "s i1, with a new deadline", c.clock.now
	if err := c.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if got, want := refused("t-a"), []string{"t-e"}; !slices.Equal(got, want) {
		t.Fatalf("refused merges of t-a once t-b's item changed %v, want %v", got, want)
	}
	if got := refused("t-b"); len(got) != 0 {
		t.Fatalf("refused merges of the changed t-b %v, want none", got)
	}
}

func TestARefusalOfATaskMergedAwayHoldsNeitherWay(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	for _, id := range []task.ID{"t-z", "t-y"} {
		c.open(id, chat("T1/C1/"+string(id)), monday.Add(-time.Hour))
	}
	c.reject("t-z", "t-x", task.VerbMerge)
	c.aliases["t-x"] = "t-y"
	refused := func(id task.ID) []string { return c.list(context.Background(), id, "task.refused_merges") }
	if got := refused("t-z"); len(got) != 0 {
		t.Errorf("refused merges of t-z %v, want none once t-x merged into t-y", got)
	}
	if got := refused("t-y"); len(got) != 0 {
		t.Errorf("refused merges of t-y %v, want none", got)
	}
}

func TestASplitPairIsNotProposedAgainUntilEitherTaskChanges(t *testing.T) {
	t.Skip("T4 split counts as refusal")
}
