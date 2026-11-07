package tasks

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/task"
)

// ErrNoView is a view the profile does not serve.
var ErrNoView = fmt.Errorf("%w: no such view", ErrArgs)

// Keep says whether a task stays in a set; it reads the task inside the pass its ctx carries.
type Keep func(ctx context.Context, e task.Task) (bool, error)

// Sets are the tasks of each of views, read in one pass over the stored tasks: each view's tasks as it lists
// them, in its order, that keep takes (every one when keep is nil); keep is called at most once per task.
func (s *Service) Sets(ctx context.Context, views []task.View, keep Keep) (map[task.View][]task.Task, error) {
	specs := make([]ViewSpec, len(views))
	for i, v := range views {
		spec, err := s.spec(v)
		if errors.Is(err, ErrArgs) {
			return nil, fmt.Errorf("%w: %q", ErrNoView, v)
		}
		specs[i] = spec
	}
	n, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	ctx = WithPass(ctx, n.pass)
	return s.sets(ctx, specs, "", n.pass.Tasks, keptOnce(ctx, keep), true)
}

// sets are the tasks of each of specs over all in area that keeps takes, in each view's order when sorted.
func (s *Service) sets(ctx context.Context, specs []ViewSpec, area string, all []task.Task, keeps func(task.Task) (bool, error), sorted bool) (map[task.View][]task.Task, error) {
	out := make(map[task.View][]task.Task, len(specs))
	for _, spec := range specs {
		q := task.Query{View: spec.ID, Area: area}
		var es []task.Task
		var err error
		switch {
		case spec.Over != task.OverTasks:
			es, err = s.sourced(ctx, spec, q)
		case sorted:
			es, err = s.list(ctx, spec, q, all)
		default:
			es, err = s.listed(ctx, spec, q, all)
		}
		if err != nil {
			return nil, err
		}
		kept := es[:0:0]
		for _, e := range es {
			k, err := keeps(e)
			if err != nil {
				return nil, err
			}
			if k {
				kept = append(kept, e)
			}
		}
		out[spec.ID] = slices.Clip(kept)
	}
	return out, nil
}

// keptOnce is keep remembered per task id; nil keeps every task.
func keptOnce(ctx context.Context, keep Keep) func(task.Task) (bool, error) {
	if keep == nil {
		return func(task.Task) (bool, error) { return true, nil }
	}
	kept := map[task.ID]bool{}
	return func(e task.Task) (bool, error) {
		k, seen := kept[e.ID]
		if !seen {
			var err error
			if k, err = keep(ctx, e); err != nil {
				return false, err
			}
			kept[e.ID] = k
		}
		return k, nil
	}
}
