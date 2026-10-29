package taskstest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	t0    = time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)
	pr    = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	other = unit.Unit{Provider: "thread", Key: "T000/1"}
)

// Full is a task with every field set.
func Full(id task.ID) task.Task {
	return task.Task{
		ID: id, State: InState("scheduled"), Category: task.CategoryDeferred, Title: "Ana owes 2 items", Notes: "notes",
		Items: []task.ItemRef{{Unit: pr, Item: "a"}, {Unit: other, Item: "b"}}, Resolved: []task.ItemRef{{Unit: pr, Item: "c"}},
		Units: []unit.Unit{pr, other},
		Props: task.Props{"focus": {Text: "on"}, "contexts": {List: []string{"@reply", "@decide"}}, "area": {Text: "OSS"},
			"due": {Time: t0.Add(24 * time.Hour)}, "owner": {Actors: []items.Person{{Key: "github:ana", Name: "Ana", Aliases: []string{"email:ana@example.com"}}}},
			"last_nudge": {Time: t0.Add(-time.Minute)}, "check_on": {Time: t0.Add(48 * time.Hour)}},
		Written: map[string]task.Written{"focus": {By: task.WriterWorkflow, At: t0.Add(time.Minute)},
			"contexts": {By: task.WriterPlugin, At: t0}, "area": {By: task.WriterPerson, At: t0}, "due": {By: task.WriterPlugin, At: t0},
			"owner": {By: task.WriterPerson, At: t0}, "last_nudge": {By: task.WriterWorkflow, At: t0}, "check_on": {By: task.WriterPerson, At: t0}},
		Parent: "t-proj", Position: 3, Sequential: true,
		Changed: true, SeenAt: t0.Add(-2 * time.Hour), Origin: task.OriginDerived,
		Created: t0.Add(-3 * time.Hour), Updated: t0,
	}
}

func position(t *testing.T, s coretasks.Store) coretasks.Since {
	t.Helper()
	p, err := s.TasksSince(context.Background(), task.Query{}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	return coretasks.Since{Epoch: p.Epoch, Rev: p.Rev}
}

func apply(t *testing.T, s coretasks.Store, es ...task.Task) {
	t.Helper()
	var cs []task.Change
	for _, e := range es {
		cs = append(cs, task.Change{Op: task.OpUpdate, Task: e})
	}
	if err := s.Apply(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
}

func ids(es []task.Task) []task.ID {
	out := []task.ID{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

// Contract runs the behavior every coretasks.Store shares; open returns an empty store.
func Contract(t *testing.T, open func(t *testing.T) coretasks.Store) {
	ctx := context.Background()
	t.Run("RoundTrip", func(t *testing.T) {
		s := open(t)
		want := Full("t-0000000000000001")
		apply(t, s, want)
		got, err := s.Task(ctx, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Same(want) || !got.Updated.Equal(want.Updated) {
			t.Fatalf("read back\n%+v\nwant\n%+v", got, want)
		}
		for _, u := range []unit.Unit{pr, other} {
			if es, err := s.TasksOf(ctx, u); err != nil || len(es) != 1 {
				t.Fatalf("tasks of %s: %v %v", u, es, err)
			}
		}
		if _, err := s.Task(ctx, "t-missing"); !errors.Is(err, coretasks.ErrNoTask) {
			t.Fatalf("a missing task: %v", err)
		}
	})
	t.Run("EmptyApplyWritesNothing", func(t *testing.T) {
		s := open(t)
		e := Full("t-0000000000000002")
		apply(t, s, e)
		before := position(t, s)
		e.Updated = e.Updated.Add(time.Hour)
		apply(t, s, e)
		if err := s.Apply(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if after := position(t, s); after != before {
			t.Fatalf("position %+v, want %+v", after, before)
		}
	})
	t.Run("TasksSince", func(t *testing.T) {
		s := open(t)
		a := task.Task{ID: "t-a", State: InState("inbox"), Category: task.CategoryInbox, Title: "a", Origin: task.OriginUser, Created: t0, Updated: t0}
		b, c := a, a
		b.ID, b.Title, b.Created = "t-b", "b", t0.Add(time.Second)
		c.ID, c.Title, c.State, c.Category = "t-c", "c", InState("done"), task.CategoryDone
		apply(t, s, a, b, c)
		q := task.Query{}
		first, err := s.TasksSince(ctx, q, coretasks.Since{})
		if err != nil || !first.Full || !slices.Equal(ids(first.Tasks), []task.ID{"t-a", "t-b"}) {
			t.Fatalf("first page %+v %v", first, err)
		}
		a.Title, b.State, b.Category = "a2", InState("done"), task.CategoryDone
		apply(t, s, a, b)
		page, err := s.TasksSince(ctx, q, coretasks.Since{Epoch: first.Epoch, Rev: first.Rev})
		if err != nil {
			t.Fatal(err)
		}
		if page.Full || !slices.Equal(ids(page.Tasks), []task.ID{"t-a", "t-b"}) || page.Tasks[0].Title != "a2" || len(page.Removed) != 0 || page.Rev <= first.Rev {
			t.Fatalf("page %+v", page)
		}
		if again, err := s.TasksSince(ctx, q, coretasks.Since{Epoch: page.Epoch, Rev: page.Rev}); err != nil || again.Full || len(again.Tasks) != 0 {
			t.Fatalf("a page at the head %+v %v", again, err)
		}
		if p, err := s.TasksSince(ctx, task.Query{All: true}, coretasks.Since{Epoch: "other", Rev: page.Rev}); err != nil || !p.Full || !slices.Equal(ids(p.Tasks), []task.ID{"t-a", "t-c", "t-b"}) {
			t.Fatalf("another epoch %+v %v", p, err)
		}
	})
	t.Run("OpenOrAll", func(t *testing.T) {
		s := open(t)
		var all []task.Task
		for i, st := range states {
			all = append(all, task.Task{ID: task.ID("e-" + st.name), State: task.State(st.name), Category: st.c, Origin: task.OriginUser,
				Created: t0.Add(time.Duration(i) * time.Second), Updated: t0})
		}
		apply(t, s, all...)
		var open []task.ID
		for _, e := range all {
			if e.Open() {
				open = append(open, e.ID)
			}
		}
		if got, err := s.Tasks(ctx, task.Query{}); err != nil || !slices.Equal(ids(got), open) {
			t.Errorf("open: %v %v, want %v", ids(got), err, open)
		}
		if got, err := s.Tasks(ctx, task.Query{All: true}); err != nil || !slices.Equal(ids(got), ids(all)) {
			t.Errorf("all: %v %v, want %v", ids(got), err, ids(all))
		}
	})
}
