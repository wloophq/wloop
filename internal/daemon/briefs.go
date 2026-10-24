package daemon

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// BriefQueue is the brief queue the daemon runs as a job.
type BriefQueue interface {
	Run(ctx context.Context) (string, error)
	// Touched wakes the queue for pull requests whose brief inputs changed.
	Touched(refs []pr.Ref)
	// Due is signaled when followed pull requests are due a background brief.
	Due() <-chan struct{}
	// Forget drops a pull request's brief, so the next run makes it again.
	Forget(ctx context.Context, ref pr.Ref) error
}

// BriefHealth is the brief queue as health reads it.
type BriefHealth interface {
	Status(ctx context.Context) (brief.Status, error)
	// Breaker is the brief breaker; open is false while briefs run.
	Breaker() (open bool, since time.Time, lastErr string, next time.Time)
}

// BriefReads are the briefs as the rows, the detail panel and the intakes
// read them.
type BriefReads interface {
	prrow.Briefs
	Views(ctx context.Context, prs ...dinv.PR) (map[pr.Ref]*brief.View, error)
	RefreshIfStale(ctx context.Context, pr dinv.PR) error
	// Attempt is the brief attempt a first turn of ref waits on.
	Attempt(ctx context.Context, ref pr.Ref) (int, error)
}

// wakeBrief hands the brief queue the pull requests whose brief inputs
// change until the returned cancel. A note of every unit runs the brief job.
func (s *Server) wakeBrief() (cancel func()) {
	b, log := s.deps.Brief, s.deps.Changes
	if b == nil || log == nil {
		return func() {}
	}
	return log.Subscribe(func(c port.Change) {
		if !c.Has(port.BriefInputs) {
			return
		}
		if c.All {
			if j, ok := s.jobs["brief"]; ok {
				j.trigger()
			}
			return
		}
		refs := refsOf(c.Units)
		if len(refs) > 0 {
			b.Touched(refs)
		}
	})
}

func refsOf(units []unit.Unit) []pr.Ref {
	var refs []pr.Ref
	for _, u := range units {
		if isPullRequest(u) {
			refs = append(refs, refOf(u))
		}
	}
	return refs
}
