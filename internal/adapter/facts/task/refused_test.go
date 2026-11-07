package facttask_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
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
	refused := func(id task.ID) []string { return c.list(context.Background(), id, "task.refused_pairs") }
	if got, want := refused("t-a"), []string{"t-b", "t-d", "t-e"}; !slices.Equal(got, want) {
		t.Fatalf("refused pairs of t-a %v, want %v: a rejected relation counts as a rejected merge does", got, want)
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
	if got, want := refused("t-a"), []string{"t-d", "t-e"}; !slices.Equal(got, want) {
		t.Fatalf("refused pairs of t-a once t-b's item changed %v, want %v", got, want)
	}
	if got, want := c.list(context.Background(), "t-a", "task.undone"), []string{"t-b", "t-d", "t-e"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-a once t-b's item changed %v, want %v: what you took apart stays undone", got, want)
	}
	if got := refused("t-b"); len(got) != 0 {
		t.Fatalf("refused merges of the changed t-b %v, want none", got)
	}
}

func TestARefusalOfATaskMergedAwayHoldsForItsSurvivor(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	for _, id := range []task.ID{"t-z", "t-y"} {
		c.open(id, chat("T1/C1/"+string(id)), monday.Add(-time.Hour))
	}
	c.reject("t-z", "t-x", task.VerbMerge)
	c.aliases["t-x"] = "t-y"
	for _, fact := range []string{"task.refused_pairs", "task.undone"} {
		if got, want := c.list(context.Background(), "t-z", fact), []string{"t-y"}; !slices.Equal(got, want) {
			t.Errorf("%s of t-z %v, want %v: t-x, which you kept apart from t-z, is in t-y now", fact, got, want)
		}
		if got, want := c.list(context.Background(), "t-y", fact), []string{"t-z"}; !slices.Equal(got, want) {
			t.Errorf("%s of t-y %v, want %v", fact, got, want)
		}
	}
}

func TestAPairStaysUndoneAfterEitherSideIsMergedAway(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	for _, id := range []task.ID{"t-ask", "t-x"} {
		c.open(id, chat("T1/C1/"+string(id)), monday.Add(-time.Hour))
	}
	c.merged("t-ask", "t-pr", monday.Add(10*time.Minute))
	c.split("t-ask", "t-pr", monday.Add(20*time.Minute))
	c.unrelated("t-old", "t-x", monday.Add(30*time.Minute))
	c.aliases["t-pr"] = "t-x"
	c.aliases["t-old"] = "t-ask"
	if got, want := c.list(context.Background(), "t-ask", "task.undone"), []string{"t-x"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-ask %v, want %v once t-pr, split from it, merged into t-x", got, want)
	}
	if got, want := c.list(context.Background(), "t-x", "task.undone"), []string{"t-ask"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-x %v, want %v", got, want)
	}
}

func TestASplitOfAChainTakesApartEveryTaskItHeld(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	for _, id := range []task.ID{"t-1", "t-2", "t-3"} {
		c.open(id, chat("T1/C1/"+string(id)), monday.Add(-time.Hour))
	}
	c.merged("t-1", "t-2", monday.Add(10*time.Minute))
	c.merged("t-2", "t-3", monday.Add(20*time.Minute))
	c.split("t-2", "t-3", monday.Add(30*time.Minute))
	c.split("t-1", "t-2", monday.Add(40*time.Minute))
	if got, want := c.list(context.Background(), "t-1", "task.undone"), []string{"t-2", "t-3"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-1 %v, want %v: t-1 was in t-3, through t-2, until the split", got, want)
	}
	if got, want := c.list(context.Background(), "t-3", "task.undone"), []string{"t-1", "t-2"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-3 %v, want %v", got, want)
	}
}

func TestASplitOutOfAMergedSurvivorTakesItApartFromTheOuterOne(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	for _, id := range []task.ID{"t-1", "t-2", "t-3"} {
		c.open(id, chat("T1/C1/"+string(id)), monday.Add(-time.Hour))
	}
	c.merged("t-1", "t-2", monday.Add(10*time.Minute))
	c.merged("t-2", "t-3", monday.Add(20*time.Minute))
	c.split("t-1", "t-3", monday.Add(30*time.Minute))
	c.aliases["t-2"] = "t-3"
	if got, want := c.list(context.Background(), "t-1", "task.undone"), []string{"t-3"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-1 %v, want %v: split out of t-3, which still holds t-2", got, want)
	}
}

// merged records, as a merge does, the record of from merged into into at.
func (c *candidateEnv) merged(from, into task.ID, at time.Time) {
	c.t.Helper()
	data, err := json.Marshal(merge.Record{From: from, Into: into, At: at})
	if err != nil {
		c.t.Fatal(err)
	}
	c.events(port.Event{Unit: task.UnitOf(from), Kind: merge.RecordKind, At: at, Data: string(data)})
}

// split records, as a split does, the verb_split events of both tasks at.
func (c *candidateEnv) split(from, into task.ID, at time.Time) {
	c.t.Helper()
	kind := task.VerbEventKind(task.VerbSplit)
	c.events(port.Event{Unit: task.UnitOf(into), Kind: kind, Actor: "person:cockpit", At: at},
		port.Event{Unit: task.UnitOf(from), Kind: kind, Actor: "person:cockpit", At: at})
}

func (c *candidateEnv) events(evs ...port.Event) {
	c.t.Helper()
	if err := sqlite.New(c.db).RecordEvents(context.Background(), evs...); err != nil {
		c.t.Fatal(err)
	}
}

func TestASplitPairIsNotProposedAgainUntilEitherTaskChanges(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	held := map[task.ID]items.Item{}
	for _, id := range []task.ID{"t-x", "t-y", "t-z", "t-w"} {
		u := chat("T1/C1/" + string(id))
		held[id] = c.ask(u, "i1", uma, meP)
		c.open(id, u, monday.Add(-time.Hour), held[id])
	}
	c.merged("t-y", "t-z", monday.Add(10*time.Minute))
	c.split("t-y", "t-z", monday.Add(20*time.Minute))
	c.merged("t-x", "t-y", monday.Add(time.Hour))
	c.merged("t-w", "t-y", monday.Add(time.Hour+time.Minute))
	c.split("t-x", "t-y", monday.Add(2*time.Hour))
	refused := func(id task.ID) []string { return c.list(context.Background(), id, "task.refused_pairs") }
	if got, want := refused("t-x"), []string{"t-y"}; !slices.Equal(got, want) {
		t.Fatalf("refused merges of the split t-x %v, want %v", got, want)
	}
	if got, want := refused("t-y"), []string{"t-x", "t-z"}; !slices.Equal(got, want) {
		t.Fatalf("refused merges of t-y %v, want %v: t-w is merged into it still", got, want)
	}
	c.clock.now = monday.Add(90 * time.Minute)
	it := held["t-z"]
	it.Summary, it.UpdatedAt = "s i1, moved", c.clock.now
	if err := c.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if got, want := refused("t-y"), []string{"t-x"}; !slices.Equal(got, want) {
		t.Fatalf("refused merges of t-y once t-z's item changed after their split %v, want %v", got, want)
	}
	c.clock.now = monday.Add(3 * time.Hour)
	it = held["t-x"]
	it.Summary, it.UpdatedAt = "s i1, moved", c.clock.now
	if err := c.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if got := refused("t-x"); len(got) != 0 {
		t.Fatalf("refused merges of t-x once its item changed %v, want none", got)
	}
}

// unrelated records, as an unrelate does, the verb_unrelate event of a's unit keying b at.
func (c *candidateEnv) unrelated(a, b task.ID, at time.Time) {
	c.t.Helper()
	c.events(port.Event{Unit: task.UnitOf(a), Kind: task.VerbEventKind(task.VerbUnrelate), Key: task.PairKey(b, at), Actor: "person:cockpit", At: at})
}

func TestAnUnrelatedPairIsRefusedUntilEitherTaskChangesAndUndoneForGood(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	held := map[task.ID]items.Item{}
	for _, id := range []task.ID{"t-p", "t-q"} {
		u := chat("T1/C1/" + string(id))
		held[id] = c.ask(u, "i1", uma, meP)
		c.open(id, u, monday.Add(-time.Hour), held[id])
	}
	c.unrelated("t-p", "t-q", monday.Add(10*time.Minute))
	for _, fact := range []string{"task.refused_pairs", "task.undone"} {
		if got, want := c.list(context.Background(), "t-q", fact), []string{"t-p"}; !slices.Equal(got, want) {
			t.Fatalf("%s of t-q %v, want %v", fact, got, want)
		}
	}
	c.clock.now = monday.Add(time.Hour)
	it := held["t-q"]
	it.Summary, it.UpdatedAt = "s i1, moved", c.clock.now
	if err := c.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if got := c.list(context.Background(), "t-p", "task.refused_pairs"); len(got) != 0 {
		t.Fatalf("refused pairs of t-p once t-q changed %v, want none", got)
	}
	if got, want := c.list(context.Background(), "t-p", "task.undone"), []string{"t-q"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-p once t-q changed %v, want %v", got, want)
	}
}

func TestReadingUndoneAloneReadsNoItemsOfThePairsOtherTask(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	other := chat("T1/C1/t-b")
	for id, u := range map[task.ID]unit.Unit{"t-a": chat("T1/C1/t-a"), "t-b": other} {
		c.open(id, u, monday.Add(-time.Hour), c.ask(u, "i1", uma, meP))
	}
	c.reject("t-a", "t-b", task.VerbMerge)
	if got, want := c.list(context.Background(), "t-a", "task.undone"), []string{"t-b"}; !slices.Equal(got, want) {
		t.Fatalf("undone of t-a %v, want %v", got, want)
	}
	if n := c.reads["items of "+other.String()]; n != 0 {
		t.Fatalf("reading task.undone loaded t-b's items %d times, as task.refused_pairs's expiry does", n)
	}
	c.list(context.Background(), "t-a", "task.refused_pairs")
	if n := c.reads["items of "+other.String()]; n == 0 {
		t.Fatal("task.refused_pairs read none of t-b's items, so this test measures nothing")
	}
}
