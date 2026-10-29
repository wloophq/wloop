package sqlite_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

type outboxSource struct{ drafts []task.Task }

func (o outboxSource) Tasks(context.Context, task.Query) ([]task.Task, error) {
	return o.drafts, nil
}

func (o outboxSource) Task(_ context.Context, id task.ID) (task.Task, bool, error) {
	for _, d := range o.drafts {
		if d.ID == id {
			return d, true, nil
		}
	}
	return task.Task{}, false, nil
}

func (o outboxSource) Count(context.Context, string) (int, error) { return len(o.drafts), nil }

var (
	countAreas    = []string{"", "OSS", "Hiring"}
	countContexts = []string{"@decide", "@reply", "@agent", "@repo-op"}
	countStates   = []task.State{task.State("inbox"), task.State("next"), task.State("next"), task.State("waiting"), task.State("scheduled"), task.State("someday"), task.State("reference"), task.State("done"), task.State("trash")}
)

// randomTasks spans areas, contexts and projects, some sequential and some ended.
func randomTasks(r *rand.Rand, n int) []task.Task {
	var out []task.Task
	var projects []task.ID
	for i := range n {
		at := taskAt.Add(time.Duration(r.IntN(10000)) * time.Minute)
		e := task.Task{ID: task.ID(fmt.Sprintf("e-%016x", i)), State: countStates[r.IntN(len(countStates))],
			Title: fmt.Sprint(i), Origin: task.OriginUser, Created: at, Updated: at}
		e.Become(e.State, taskstest.BundledStates)
		if e.State == "scheduled" && r.IntN(2) == 0 {
			e.Snooze = task.Snooze{OnActivity: true}
		}
		e.SetProp(task.PropArea, task.Value{Text: countAreas[r.IntN(len(countAreas))]}, task.WriterPerson, at)
		if r.IntN(4) == 0 {
			e.SetProp(task.PropFocus, task.Value{Text: task.FocusOn}, task.WriterPerson, at)
		}
		var cxs []string
		for _, c := range countContexts {
			if r.IntN(3) == 0 {
				cxs = append(cxs, c)
			}
		}
		e.SetProp(task.PropContexts, task.Value{List: cxs}, task.WriterPerson, at)
		switch {
		case r.IntN(8) == 0:
			e.Sequential = r.IntN(2) == 0
			e.SetProp(task.PropKind, task.Value{Text: task.KindProject}, task.WriterWorkflow, at)
			projects = append(projects, e.ID)
		case len(projects) > 0 && r.IntN(2) == 0:
			e.Parent, e.Position = projects[r.IntN(len(projects))], r.IntN(5)
		}
		out = append(out, e)
	}
	return out
}

func TestCountersEqualTheirLists(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(15, 2))
	ctx := context.Background()
	for round := range 20 {
		tasks := randomTasks(r, 20+r.IntN(80))
		drafts := make([]task.Task, r.IntN(4))
		t.Run(fmt.Sprint("round", round), func(t *testing.T) {
			t.Parallel()
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "counts.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := db.Tasks()
			applyTasks(t, store, tasks...)
			svc := coretasks.New(coretasks.Deps{Store: store, Clock: fixedNow{taskAt}, States: taskstest.BundledStates})
			svc.RegisterView(task.ViewOutbox, "draft:", outboxSource{drafts: drafts})
			for _, area := range countAreas {
				c, err := svc.Counts(ctx, area)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range append([]task.View{task.ViewOutbox}, task.StoredViews...) {
					list, err := svc.Tasks(ctx, task.Query{View: v, Area: area})
					if err != nil {
						t.Fatal(err)
					}
					if got, ok := c.Views[v]; !ok || got != len(list) {
						t.Errorf("round %d area %q view %s: counter %d (%v), list %d", round, area, v, got, ok, len(list))
					}
				}
				if c.Badge != c.Views[task.ViewInbox]+c.Views[task.ViewOutbox] {
					t.Errorf("round %d area %q: badge %d", round, area, c.Badge)
				}
				next, err := svc.Tasks(ctx, task.Query{View: task.ViewNext})
				if err != nil {
					t.Fatal(err)
				}
				for _, a := range countAreas[1:] {
					n := len(task.Select(next, task.Query{View: task.ViewNext, Area: a}))
					if c.Areas[a] != n {
						t.Errorf("round %d area %q: Areas[%s] %d, list %d", round, area, a, c.Areas[a], n)
					}
				}
				for _, cx := range countContexts {
					n := len(task.Select(next, task.Query{View: task.ViewNext, Area: area, Contexts: []string{cx}}))
					if c.Contexts[cx] != n {
						t.Errorf("round %d area %q: Contexts[%s] %d, list %d", round, area, cx, c.Contexts[cx], n)
					}
				}
			}
		})
	}
}

type fixedNow struct{ t time.Time }

func (f fixedNow) Now() time.Time { return f.t }

func BenchmarkCounts(b *testing.B) {
	db, err := sqlite.Open(filepath.Join(b.TempDir(), "bench.db"), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	es := randomTasks(rand.New(rand.NewPCG(500, 1)), 500)
	var cs []task.Change
	for _, e := range es {
		cs = append(cs, task.Change{Op: task.OpCreate, Task: e})
	}
	if err := db.Tasks().Apply(context.Background(), cs); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := db.Tasks().Counts(context.Background(), "OSS"); err != nil {
			b.Fatal(err)
		}
	}
}
