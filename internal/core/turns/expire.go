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

// ProcessGone says whether the last Census found the active session's process
// missing from two complete censuses of its manager's same runtime, at least
// FastJob apart.
func (s *Service) ProcessGone(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gone[id]
}

// Census reads every manager's census and marks the active sessions whose
// manager's complete census misses their process; it changes nothing.
// ProcessGone then names the ones missed by two censuses at least FastJob apart.
// A manager that cannot list completely marks none of its sessions.
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
		if sess.State() == turn.SessionDormant && listed {
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
	for _, sess := range open {
		runtime := c.RuntimeOf(sess.Manager)
		if sess.State() != turn.SessionActive || !c.CompleteOf(sess.Manager) || runtime == "" {
			if m, ok := s.goneSeen[sess.ID]; ok && sess.State() == turn.SessionActive {
				marks[sess.ID], gone[sess.ID] = m, s.gone[sess.ID]
			}
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
	s.goneSeen = marks
	for _, h := range due {
		gone[h] = true
	}
	s.gone = gone
	return nil
}
