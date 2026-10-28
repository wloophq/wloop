package daemon

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// BriefQueue is the brief queue the daemon runs as a job.
type BriefQueue interface {
	Run(ctx context.Context) (string, error)
	// Touched wakes the queue for units whose brief inputs changed.
	Touched(units []unit.Unit)
	// Due is signaled when followed units are due a background brief.
	Due() <-chan struct{}
	// Forget drops a unit's brief, so the next run makes it again.
	Forget(ctx context.Context, u unit.Unit) error
}

// BriefHealth is the brief queue as health reads it.
type BriefHealth interface {
	Status(ctx context.Context) (brief.Status, error)
	// Breaker is the brief breaker; open is false while briefs run.
	Breaker() (open bool, since time.Time, lastErr string, next time.Time)
}

// BriefAttempts are the brief attempts the intakes wait on.
type BriefAttempts interface {
	// Attempt is the brief attempt a first turn of u waits on.
	Attempt(ctx context.Context, u unit.Unit) (int, error)
}

// wakeBrief hands the brief queue the forge's units whose brief inputs
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
		var units []unit.Unit
		for _, u := range c.Units {
			if s.forge(u) {
				units = append(units, u)
			}
		}
		if len(units) > 0 {
			b.Touched(units)
		}
	})
}
