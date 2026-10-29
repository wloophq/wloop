package daemon

import (
	"context"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/catalog"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/turn"
)

// bundledLoaded is the bundled profile.
func bundledLoaded() *intentload.Loaded {
	l, err := turn.Bundled()
	if err != nil {
		panic(err)
	}
	return l
}

// withLens is d placing tasks in the views of the profile loaded reads, the bundled one when nil, as serve
// wires it; facts reads a task's facts for a view that reads more than its own fields hold, nil none.
func withLens(d coretasks.Deps, loaded func() *intentload.Loaded, facts func(context.Context, task.Task) (workflow.Facts, error)) coretasks.Deps {
	if loaded == nil {
		loaded = bundledLoaded
	}
	l := catalog.Lens{Loaded: loaded, Facts: facts, Now: func() time.Time { return catalogNow }}
	d.Views, d.ViewEval, d.ViewSort = func() coretasks.Views {
		def := loaded().Views
		var out coretasks.Views
		for _, v := range def.Views {
			if catalog.Lists(def, v.ID) {
				out.List = append(out.List, coretasks.ViewSpec{ID: task.View(v.ID), Over: v.Over, Paged: catalog.Paged(def, v.ID), HideDisabled: v.HideDisabled})
			}
		}
		for _, id := range def.Badge {
			out.Badge = append(out.Badge, task.View(id))
		}
		for _, st := range def.Review {
			out.Review = append(out.Review, task.View(st.View))
		}
		return out
	}, l.Eval, l.Sort
	return d
}

// lensFacts are the task subject's facts over db, as serve reads them for a view, under the profile loaded reads.
func lensFacts(db *sqlite.DB, loaded func() *intentload.Loaded, now time.Time) func(context.Context, task.Task) (workflow.Facts, error) {
	p := facttask.New(facttask.Deps{Tasks: db.Tasks(), Items: db.Items(), Gates: db.Gates(func() time.Time { return now }), Wakes: db.Wakes(),
		Clock: fixedClock(now), Me: func() task.Me { return meID }, OwedBy: catalog.OwedByOf(loaded),
		Props:      func(t task.Task) workflow.Facts { return catalog.PropFacts(t, loaded().Tasks) },
		Extraction: psource.NewSnapshots(db.Snapshots()).Unextracted,
		RoleProp: func(r task.Role) string {
			name, _ := loaded().Tasks.PropOf(r)
			return name
		}})
	return func(ctx context.Context, t task.Task) (workflow.Facts, error) {
		read := func() (workflow.Facts, error) { return p.TaskFacts(ctx, t) }
		if pass, ok := coretasks.PassOf(ctx); ok {
			return pass.Facts(t.ID, read)
		}
		return read()
	}
}
