package turns

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
)

// goneMark is the census that last missed a session.
type goneMark struct {
	at      time.Time
	runtime string
}

// orphanMark is how long a census has seen an active session's process orphaned.
type orphanMark struct {
	since   time.Time
	runtime string
	// epoch is the renderer graph epoch of the latest reading, epochAt the first
	// reading in it; crossed says the orphan outlived a change of epoch.
	epoch   string
	epochAt time.Time
	crossed bool
	durable bool
}

// DefaultOrphanAfter is how long a process must stay orphaned, with no output
// since, before its orphan is durable without a renderer restart to prove it.
const DefaultOrphanAfter = 2 * time.Hour

// ProcessGone says whether the last Census found the active session's process
// missing from two complete censuses of its manager's same runtime, at least
// FastJob apart.
func (s *Service) ProcessGone(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gone[id]
}

// OrphanDurable says whether the last Census proved the active session's
// process a durable orphan: complete readings of the same runtime have listed
// it orphaned since it was first seen so, the latest renderer graph epoch held
// across two readings at least FastJob apart, and the orphan either outlived
// a change of that epoch, when the app's windows reattached the processes
// they hold, or stayed orphaned for OrphanAfter with no output since.
func (s *Service) OrphanDurable(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.orphanSeen[id].durable
}

func (s *Service) orphanAfter() time.Duration {
	if s.OrphanAfter > 0 {
		return s.OrphanAfter
	}
	return DefaultOrphanAfter
}

// Census reads every manager's census and marks the active sessions whose
// manager's complete census misses their process, and those it lists
// orphaned; it changes nothing but waking a dormant session whose process is
// listed again in a window of its app. ProcessGone then names the ones missed
// by two censuses at least FastJob apart, and OrphanDurable the orphans proven
// durable. A manager that cannot list completely marks none of its sessions.
func (s *Service) Census(ctx context.Context) error {
	c, err := s.Sessions.Census(ctx)
	if err != nil {
		return err
	}
	at := c.At
	if at.IsZero() {
		at = s.now()
	}
	open, err := s.Registry.Sessions(ctx)
	if err != nil {
		return err
	}

	for _, sess := range open {
		p, listed := c.Processes[sess.ID]
		_, orphan := c.Orphans[sess.ID]
		if sess.State() == turn.SessionDormant && listed && !orphan {
			if err := s.Woken(ctx, sess.ID, p.Manager, p.Handle); err != nil {
				return err
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var due []string
	marks := map[string]goneMark{}
	gone := map[string]bool{}
	orphans := map[string]orphanMark{}
	for _, sess := range open {
		runtime := c.RuntimeOf(sess.Manager)
		if sess.State() != turn.SessionActive || !c.CompleteOf(sess.Manager) || runtime == "" {
			if m, ok := s.goneSeen[sess.ID]; ok && sess.State() == turn.SessionActive {
				marks[sess.ID], gone[sess.ID] = m, s.gone[sess.ID]
			}
			if m, ok := s.orphanSeen[sess.ID]; ok && sess.State() == turn.SessionActive {
				m.durable = false
				orphans[sess.ID] = m
			}
			continue
		}
		if last, ok := c.Orphans[sess.ID]; ok {
			if m, ok := s.orphanAt(sess.ID, runtime, c.EpochOf(sess.Manager), last, at); ok {
				orphans[sess.ID] = m
			}
		}
		if _, listed := c.Sessions[sess.ID]; listed || sess.StartedAt.After(at) {
			continue
		}
		m, ok := s.goneSeen[sess.ID]
		switch {
		case !ok || m.runtime != runtime:
			marks[sess.ID] = goneMark{at: at, runtime: runtime}
		case at.After(m.at) && at.Sub(m.at) >= s.FastJob:
			due = append(due, sess.ID)
			marks[sess.ID] = m
		default:
			marks[sess.ID] = m
		}
	}
	s.goneSeen, s.orphanSeen = marks, orphans
	for _, h := range due {
		gone[h] = true
	}
	s.gone = gone
	return nil
}

// orphanAt is the session's orphan mark after a complete reading at at that
// lists its process orphaned with last output last; false when the reading
// knows no epoch, which proves nothing and drops the mark.
func (s *Service) orphanAt(id, runtime, epoch string, last, at time.Time) (orphanMark, bool) {
	if epoch == "" {
		return orphanMark{}, false
	}
	m, ok := s.orphanSeen[id]
	switch {
	case !ok || m.runtime != runtime:
		return orphanMark{since: at, runtime: runtime, epoch: epoch, epochAt: at}, true
	case m.epoch != epoch:
		m.epoch, m.epochAt, m.crossed = epoch, at, true
	}
	stable := at.Sub(m.epochAt) >= s.FastJob
	quiet := at.Sub(m.since) >= s.orphanAfter() && !last.After(m.since)
	m.durable = stable && (m.crossed || quiet)
	return m, true
}
