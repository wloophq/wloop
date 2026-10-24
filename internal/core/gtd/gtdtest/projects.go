package gtdtest

import (
	"context"
	"slices"
	"sync"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

// Projects is an in-memory coregtd.ProjectStore over the projects of Entries.
type Projects struct {
	Entries *Store
	mu      sync.Mutex
	runs    []gtd.ReviewRun
}

var _ coregtd.ProjectStore = (*Projects)(nil)

func (p *Projects) SlugTaken(ctx context.Context, slug string) (bool, error) {
	p.Entries.mu.Lock()
	defer p.Entries.mu.Unlock()
	for _, e := range p.Entries.entries {
		if e.Kind == gtd.KindProject && e.Slug == slug {
			return true, nil
		}
	}
	return false, nil
}

func (p *Projects) CurrentRun(ctx context.Context) (gtd.ReviewRun, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.runs) - 1; i >= 0; i-- {
		if p.runs[i].FinishedAt.IsZero() {
			return clone(p.runs[i]), true, nil
		}
	}
	return gtd.ReviewRun{}, false, nil
}

func (p *Projects) PutRun(ctx context.Context, r gtd.ReviewRun) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := slices.IndexFunc(p.runs, func(x gtd.ReviewRun) bool { return x.ID == r.ID }); i >= 0 {
		p.runs[i] = clone(r)
		return nil
	}
	p.runs = append(p.runs, clone(r))
	return nil
}

// Runs are every run put, oldest first.
func (p *Projects) Runs() []gtd.ReviewRun {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]gtd.ReviewRun, len(p.runs))
	for i, r := range p.runs {
		out[i] = clone(r)
	}
	return out
}

func clone(r gtd.ReviewRun) gtd.ReviewRun {
	r.Done = slices.Clone(r.Done)
	return r
}
