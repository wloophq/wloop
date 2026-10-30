package taskstest

import (
	"context"
	"slices"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// ProjectsContract is the suite every ProjectStore passes; open gives an empty task store and the project
// store over it.
func ProjectsContract(t *testing.T, open func(t *testing.T) (coretasks.Store, coretasks.ProjectStore)) {
	ctx := context.Background()
	project := func(id task.ID, st string) task.Task {
		c, _ := BundledStates.CategoryOf(st)
		p := task.Task{ID: id, State: InState(st), Category: c, Title: "P " + string(id), Origin: task.OriginUser, Sequential: true,
			ReviewEvery: 14 * 24 * time.Hour, ReviewedAt: t0.Add(-time.Hour), Created: t0, Updated: t0}
		p.SetProp(task.PropKind, task.Value{Text: task.KindProject}, task.WriterWorkflow, t0)
		return p
	}

	t.Run("project fields round-trip", func(t *testing.T) {
		s, _ := open(t)
		p := project("t-p1", "next")
		apply(t, s, p)
		got, err := s.Task(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Same(p) {
			t.Fatalf("read %+v, want %+v", got, p)
		}
		before := position(t, s)
		apply(t, s, p)
		if after := position(t, s); after != before {
			t.Errorf("an Apply of the same project moved the position from %v to %v", before, after)
		}
		p.ReviewedAt, p.Updated = t0, t0.Add(time.Minute)
		apply(t, s, p)
		if got, _ := s.Task(ctx, p.ID); !got.ReviewedAt.Equal(t0) {
			t.Errorf("reviewed at %v, want %v", got.ReviewedAt, t0)
		}
		ps, err := s.Tasks(ctx, task.Query{})
		if err != nil || len(ps) != 1 || ps[0].ID != p.ID || ps[0].ReviewEvery != 14*24*time.Hour {
			t.Errorf("open tasks %+v, %v", ps, err)
		}
	})

	t.Run("review runs", func(t *testing.T) {
		_, ps := open(t)
		if _, ok, err := ps.CurrentRun(ctx); ok || err != nil {
			t.Fatalf("a fresh store has a current run: %v, %v", ok, err)
		}
		r := task.ReviewRun{ID: "r1", StartedAt: t0, Done: []string{"outbox"}}
		if err := ps.PutRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		r.Done = append(r.Done, "inbox")
		if err := ps.PutRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		got, ok, err := ps.CurrentRun(ctx)
		if err != nil || !ok || got.ID != "r1" || !got.StartedAt.Equal(t0) || !slices.Equal(got.Done, []string{"outbox", "inbox"}) {
			t.Fatalf("current %+v, %v, %v", got, ok, err)
		}
		r.FinishedAt = t0.Add(time.Hour)
		if err := ps.PutRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := ps.CurrentRun(ctx); ok {
			t.Error("a finished run is still current")
		}
		if err := ps.PutRun(ctx, task.ReviewRun{ID: "r2", StartedAt: t0.Add(2 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if got, ok, _ := ps.CurrentRun(ctx); !ok || got.ID != "r2" || len(got.Done) != 0 {
			t.Errorf("current after a new start %+v, %v", got, ok)
		}
	})
}
