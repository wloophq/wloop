package taskstest

import (
	"context"
	"slices"
	"sync"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Projects is an in-memory coretasks.ProjectStore: the Review's runs.
type Projects struct {
	mu   sync.Mutex
	runs []task.ReviewRun
}

var _ coretasks.ProjectStore = (*Projects)(nil)

func (p *Projects) CurrentRun(ctx context.Context) (task.ReviewRun, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.runs) - 1; i >= 0; i-- {
		if p.runs[i].FinishedAt.IsZero() {
			return clone(p.runs[i]), true, nil
		}
	}
	return task.ReviewRun{}, false, nil
}

func (p *Projects) PutRun(ctx context.Context, r task.ReviewRun) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := slices.IndexFunc(p.runs, func(x task.ReviewRun) bool { return x.ID == r.ID }); i >= 0 {
		p.runs[i] = clone(r)
		return nil
	}
	p.runs = append(p.runs, clone(r))
	return nil
}

// Runs are every run put, oldest first.
func (p *Projects) Runs() []task.ReviewRun {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]task.ReviewRun, len(p.runs))
	for i, r := range p.runs {
		out[i] = clone(r)
	}
	return out
}

func clone(r task.ReviewRun) task.ReviewRun {
	r.Done = slices.Clone(r.Done)
	return r
}
