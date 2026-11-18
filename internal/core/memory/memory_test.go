package memory_test

import (
	"context"
	"slices"
	"testing"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/memory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	t0     = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	pr     = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread = unit.Unit{Provider: "slack", Key: "C1/1700000000.000100"}
	other  = unit.Unit{Provider: unit.GitHub, Key: "acme/web#3"}
)

type fakeTasks map[task.ID]task.Task

func (f fakeTasks) Task(_ context.Context, id task.ID) (task.Task, error) { return f[id], nil }

func (f fakeTasks) TasksOf(_ context.Context, u unit.Unit) ([]task.Task, error) {
	var out []task.Task
	for _, e := range f {
		if slices.Contains(e.Units, u) || slices.ContainsFunc(e.Items, func(r task.ItemRef) bool { return r.Unit == u }) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b task.Task) int { return a.Created.Compare(b.Created) })
	return out, nil
}

type fakeMerges map[task.ID][]task.ID

func (f fakeMerges) MergedInto(_ context.Context, id task.ID) ([]task.ID, error) { return f[id], nil }

type fakeBooks map[unit.Unit][]turn.Record

func (f fakeBooks) Books(_ context.Context, us ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	out := map[unit.Unit]turn.Book{}
	for _, u := range us {
		out[u] = turn.Book{Unit: u, Records: f[u]}
	}
	return out, nil
}

type fakeItems struct {
	items map[unit.Unit][]items.Item
	log   map[unit.Unit][]coreitems.LogEntry
}

func (f fakeItems) Items(_ context.Context, u unit.Unit) ([]items.Item, error) {
	return f.items[u], nil
}

func (f fakeItems) Log(_ context.Context, u unit.Unit, _ time.Time) ([]coreitems.LogEntry, error) {
	return f.log[u], nil
}

func rec(at time.Time, summary, learned string) turn.Record {
	return turn.Record{At: at, Record: record.Record{State: record.Done, Summary: summary, Learned: learned}}
}

func TestTheLearnedEntriesSpanTheTasksUnitsAndTheMergedTasks(t *testing.T) {
	merged := task.Task{ID: "t-merged", Category: task.CategoryDone, Created: t0}
	e := task.Task{ID: "t-main", Category: task.CategoryActive, Units: []unit.Unit{pr}, Created: t0, OpenChanged: t0,
		Items: []task.ItemRef{{Unit: thread, Item: "i1"}}}
	books := fakeBooks{
		pr:                     {rec(t0.Add(1*time.Hour), "pr one", "the build needs Go 1.24"), rec(t0.Add(5*time.Hour), "pr two", "")},
		thread:                 {rec(t0.Add(2*time.Hour), "thread", "the owner wants it by Friday")},
		task.UnitOf(merged.ID): {rec(t0.Add(3*time.Hour), "merged", "a repro lives in the PR description")},
		other:                  {rec(t0.Add(4*time.Hour), "elsewhere", "not this task's")},
	}
	s := memory.Service{Tasks: fakeTasks{e.ID: e, merged.ID: merged}, Merges: fakeMerges{e.ID: {merged.ID}}, Books: books}
	got, err := s.Of(context.Background(), thread)
	if err != nil {
		t.Fatal(err)
	}
	want := []memory.Entry{
		{Unit: task.UnitOf(merged.ID), At: t0.Add(3 * time.Hour), Text: "a repro lives in the PR description"},
		{Unit: thread, At: t0.Add(2 * time.Hour), Text: "the owner wants it by Friday"},
		{Unit: pr, At: t0.Add(1 * time.Hour), Text: "the build needs Go 1.24"},
	}
	if got.Task != e.ID || !slices.Equal(got.Learned, want) {
		t.Fatalf("memory %+v", got)
	}
	if got.Outcome != nil {
		t.Errorf("a task never reopened has an outcome: %+v", got.Outcome)
	}
}

func TestAUnitNoTaskHoldsKeepsItsOwnEntries(t *testing.T) {
	s := memory.Service{Tasks: fakeTasks{}, Books: fakeBooks{other: {rec(t0, "s", "only here")}}}
	got, err := s.Of(context.Background(), other)
	if err != nil || got.Task != "" || len(got.Learned) != 1 || got.Learned[0].Text != "only here" {
		t.Fatalf("memory %+v, err %v", got, err)
	}
}

func TestAReopenedTaskCarriesTheOutcomeItClosedWith(t *testing.T) {
	reopened := t0.Add(48 * time.Hour)
	e := task.Task{ID: "t-main", Category: task.CategoryActive, Units: []unit.Unit{pr}, Created: t0, OpenChanged: reopened,
		Items: []task.ItemRef{{Unit: thread, Item: "i2"}}, Resolved: []task.ItemRef{{Unit: thread, Item: "i1"}}}
	books := fakeBooks{
		pr:     {rec(t0.Add(time.Hour), "first try", ""), rec(t0.Add(20*time.Hour), "merged the fix and replied", "")},
		thread: {rec(reopened.Add(time.Hour), "after the reopen", "")},
	}
	resolve := func(item string, r items.Resolution, at time.Time) coreitems.LogEntry {
		return coreitems.LogEntry{Unit: thread, Item: item, Op: items.Op{Op: items.OpResolve, Item: item, Resolution: r}, Outcome: coreitems.OutcomeApplied, At: at}
	}
	its := fakeItems{
		items: map[unit.Unit][]items.Item{thread: {{ID: "i1", Summary: "fix the login"}, {ID: "i2", Summary: "answer the follow-up"}}},
		log: map[unit.Unit][]coreitems.LogEntry{thread: {
			resolve("i1", items.Declined, t0.Add(10*time.Hour)),
			resolve("i1", items.Fixed, t0.Add(19*time.Hour)),
			resolve("i2", items.Answered, t0.Add(21*time.Hour)),
			{Unit: thread, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2", Resolution: items.Moot}, Outcome: "refused", At: t0.Add(22 * time.Hour)},
			resolve("i2", items.Done, reopened.Add(2*time.Hour)),
		}},
	}
	s := memory.Service{Tasks: fakeTasks{e.ID: e}, Books: books, Items: its}
	got, err := s.Of(context.Background(), pr)
	if err != nil {
		t.Fatal(err)
	}
	want := memory.Outcome{ReopenedAt: reopened, Summary: "merged the fix and replied", Items: []memory.Resolved{
		{Unit: thread, Item: "i1", Summary: "fix the login", Resolution: items.Fixed},
		{Unit: thread, Item: "i2", Summary: "answer the follow-up", Resolution: items.Answered},
	}}
	if got.Outcome == nil || got.Outcome.ReopenedAt != want.ReopenedAt || got.Outcome.Summary != want.Summary || !slices.Equal(got.Outcome.Items, want.Items) {
		t.Fatalf("outcome %+v, want %+v", got.Outcome, want)
	}
}

func TestAClosedTaskHasNoOutcomeYet(t *testing.T) {
	e := task.Task{ID: "t-main", Category: task.CategoryDone, Units: []unit.Unit{pr}, Created: t0, OpenChanged: t0.Add(time.Hour)}
	s := memory.Service{Tasks: fakeTasks{e.ID: e}, Books: fakeBooks{pr: {rec(t0, "done", "")}}}
	got, err := s.OfTask(context.Background(), e)
	if err != nil || got.Outcome != nil {
		t.Fatalf("outcome %+v, err %v", got.Outcome, err)
	}
}
