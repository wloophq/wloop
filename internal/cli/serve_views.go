package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/catalog"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// lensViews are the views of the profile loaded reads that list something, as the tasks service lists them,
// and the views its badge and review name.
func lensViews(loaded func() *intentload.Loaded) func() coretasks.Views {
	return func() coretasks.Views {
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
	}
}

// lens is the profile's lens over the task subject's facts, each task's read once per pass.
func (e *Env) lens() catalog.Lens {
	return catalog.Lens{Loaded: e.loadedIntents, Now: e.Now, Facts: func(ctx context.Context, t task.Task) (workflow.Facts, error) {
		read := func() (workflow.Facts, error) { return e.factsOfTask(ctx, t) }
		if p, ok := coretasks.PassOf(ctx); ok {
			return p.Facts(t.ID, read)
		}
		return read()
	}}
}
