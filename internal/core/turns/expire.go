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
	// since is the first reading that listed it orphaned, at the latest.
	since, at time.Time
	runtime   string
	// epoch is the renderer graph epoch of the latest reading, epochAt the first
	// reading in it; shownAt is the first reading in it that showed some
	// terminal in a window of the app, zero for none.
	epoch   string
	epochAt time.Time
	shownAt time.Time
	// crossed says the orphan outlived a change of epoch after it was seen
	// orphaned for FastJob in an epoch whose windows showed some terminal.
	crossed bool
	// quietSince starts the run of readings that all showed some terminal,
	// zero after one that showed none or could not tell.
	quietSince time.Time
	durable    bool
	// active says the session was active at the reading, not dormant.
	active bool
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

// ProcessOrphaned says whether complete censuses of the same runtime have
// listed the active session's process orphaned, with no agent in it, for at
// least FastJob: no window of the app shows it. It proves nothing about the
// process staying so; MarkDormant retires such a session without ending it.
func (s *Service) ProcessOrphaned(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.orphanSeen[id]
	return ok && m.active && !m.at.IsZero() && m.at.Sub(m.since) >= s.FastJob
}

// OrphanDurable says whether the last Census proved the open session's
// process a durable orphan: complete readings of the same runtime have listed
// it orphaned since it was first seen so, the latest renderer graph epoch held
// across two readings at least FastJob apart while the app's windows showed
// some terminal, and the orphan either outlived a change of epoch after
// staying orphaned for FastJob in an epoch whose windows showed some
// terminal, or stayed orphaned for OrphanAfter, every reading showing some
// terminal, with a known last output from before that run.
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
		if !c.CompleteOf(sess.Manager) || runtime == "" {
			if m, ok := s.goneSeen[sess.ID]; ok && sess.State() == turn.SessionActive {
				marks[sess.ID], gone[sess.ID] = m, s.gone[sess.ID]
			}
			if m, ok := s.orphanSeen[sess.ID]; ok {
				m.durable, m.quietSince = false, time.Time{}
				orphans[sess.ID] = m
			}
			continue
		}
		if last, ok := c.Orphans[sess.ID]; ok {
			m := s.orphanAt(sess.ID, runtime, c.EpochOf(sess.Manager), c.ShownOf(sess.Manager), last, at)
			m.active = sess.State() == turn.SessionActive
			orphans[sess.ID] = m
		}
		if sess.State() != turn.SessionActive {
			continue
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
// lists its process orphaned with last output last, in epoch, whose windows
// showed some terminal when shown. A reading with no epoch keeps the mark for
// ProcessOrphaned and proves nothing durable.
func (s *Service) orphanAt(id, runtime, epoch string, shown bool, last, at time.Time) orphanMark {
	m, ok := s.orphanSeen[id]
	if !ok || m.runtime != runtime {
		m = orphanMark{since: at, runtime: runtime}
	}
	prev := m.at
	m.at, m.durable = at, false
	if epoch == "" {
		m.epoch, m.epochAt, m.shownAt, m.crossed, m.quietSince = "", time.Time{}, time.Time{}, false, time.Time{}
		return m
	}
	if m.epoch != epoch {
		if m.epoch != "" && !m.shownAt.IsZero() && prev.Sub(m.shownAt) >= s.FastJob {
			m.crossed = true
		}
		m.epoch, m.epochAt, m.shownAt = epoch, at, time.Time{}
	}
	switch {
	case !shown:
		m.quietSince = time.Time{}
	case m.shownAt.IsZero():
		m.shownAt = at
	}
	if shown && m.quietSince.IsZero() {
		m.quietSince = at
	}
	stable := shown && at.Sub(m.epochAt) >= s.FastJob
	quiet := shown && at.Sub(m.quietSince) >= s.orphanAfter() && !last.IsZero() && !last.After(m.quietSince)
	m.durable = stable && (m.crossed || quiet)
	return m
}
