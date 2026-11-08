package turns

import (
	"context"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
)

// RefusalOrphanUnproven refuses closing a process the census has not proven
// a durable orphan (see OrphanDurable).
const RefusalOrphanUnproven = "its process is not proven a lasting orphan"

// RefusalOrphanMoved refuses closing an orphan a fresh census no longer lists
// as the one the sweep proved.
const RefusalOrphanMoved = "its process is no longer the orphan the census proved"

const sessionDormant = "the session is dormant"

// RefusalOrphanLingers holds the close of a dormant session whose process the
// census still lists orphaned and has not proven a lasting orphan: a window
// may reclaim the pane, and the census then wakes the session.
const RefusalOrphanLingers = "its process lingers orphaned and may come back; it closes once the process is gone, or after cleanup.orphan_hold"

// ProcessLingers says whether a fresh census lists a process of the session,
// orphaned or not, or cannot tell: waking it with a new process would run
// two on one agent session.
func (s *Service) ProcessLingers(ctx context.Context, id string) (bool, error) {
	sess, ok, err := s.Registry.Session(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	s.refresh()
	c, err := s.Sessions.Census(ctx)
	if err != nil {
		return false, err
	}
	_, listed := c.Sessions[id]
	return listed || !c.CompleteOf(sess.Manager), nil
}

// orphanLingers says whether the last Census listed the dormant session's
// process orphaned without proving it a lasting orphan.
func (s *Service) orphanLingers(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.orphanSeen[id]
	held := !m.attachedSince.IsZero() && m.at.Sub(m.attachedSince) >= s.orphanHold()
	return ok && !m.durable && !held
}

// DefaultOrphanHold is how long a dormant session's close waits on its
// lingering orphan, the app's windows attached throughout, before it closes
// the record anyway.
const DefaultOrphanHold = 24 * time.Hour

func (s *Service) orphanHold() time.Duration {
	if s.OrphanHold > 0 {
		return s.OrphanHold
	}
	return DefaultOrphanHold
}

// CloseOrphanGone says whether why, a CloseOrphanRefusal of id, holds for
// good: there is no such session, or it is closed or dormant, so no process
// of it is left to close.
func CloseOrphanGone(id, why string) bool { return CloseGone(id, why) || why == sessionDormant }

// CloseOrphanRefusal is why CloseOrphan may not end the session's orphaned
// process, "" when it may: the guards of session.close but the shown terminal
// it no longer has, and the census's proof that the orphan is durable. It
// writes nothing; an error is a read that failed.
func (s *Service) CloseOrphanRefusal(ctx context.Context, id string) (string, error) {
	why, _, err := s.orphanCheck(ctx, id)
	return why, err
}

// CloseOrphan ends the session's orphaned process when CloseOrphanRefusal
// allows it and a fresh census still lists the process orphaned with no agent
// in the runtime and epoch the proof was made in. It records nothing on the
// session: the censuses that then miss the process mark it dormant.
func (s *Service) CloseOrphan(ctx context.Context, id string) error {
	s.refresh()
	why, w, err := s.orphanCheck(ctx, id)
	if err != nil {
		return err
	}
	if why == "" && !w.sess.PromptSeenAt.IsZero() {
		at, ok, _ := s.lastUserPrompt(ctx, w.sess)
		why = w.transcriptRefusal(at, ok)
	}
	if why == "" {
		if why, err = s.orphanStill(ctx, w.sess); err != nil {
			return err
		}
	}
	switch {
	case CloseOrphanGone(id, why):
		return Gone{Reason: why}
	case why != "":
		return Refusal{Reason: why}
	}
	return s.Sessions.Close(ctx, id)
}

// orphanStill refuses when a fresh census does not list the session's process
// orphaned, complete, in the runtime and epoch its durable mark was proven in.
func (s *Service) orphanStill(ctx context.Context, sess turn.Session) (string, error) {
	c, err := s.Sessions.Census(ctx)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	m := s.orphanSeen[sess.ID]
	s.mu.Unlock()
	if _, orphan := c.Orphans[sess.ID]; !orphan || !c.CompleteOf(sess.Manager) || c.RuntimeOf(sess.Manager) != m.runtime || c.EpochOf(sess.Manager) != m.epoch {
		return RefusalOrphanMoved, nil
	}
	return "", nil
}

func (s *Service) orphanCheck(ctx context.Context, id string) (string, promptWindow, error) {
	sess, ok, err := s.Registry.Session(ctx, id)
	switch {
	case err != nil:
		return "", promptWindow{}, err
	case !ok:
		return noSession(id), promptWindow{}, nil
	case !sess.ClosedAt.IsZero():
		return sessionClosed, promptWindow{}, nil
	case sess.State() == turn.SessionDormant:
		return sessionDormant, promptWindow{}, nil
	}
	if !s.OrphanDurable(id) {
		return RefusalOrphanUnproven, promptWindow{}, nil
	}
	return s.paneless(ctx, sess)
}

// paneless is why a session whose process no window shows may not be ended
// or retired, "" when it may: session.close's guards that need no shown
// terminal. No turn is sent or started on it, no agent of it works or waits
// for permission (read by its pane, else by any agent of its workspace), its
// workspace is not the one open in the app, and no prompt of yours came after
// the loop's last turn, or while it was idle.
func (s *Service) paneless(ctx context.Context, sess turn.Session) (string, promptWindow, error) {
	if s.typingInto(sess.ID) {
		return "its turn is " + string(turn.Sent), promptWindow{}, nil
	}
	newest, err := s.sessionTurns(ctx, sess.ID)
	if err != nil {
		return "", promptWindow{}, err
	}
	for _, t := range newest.open {
		if t.State == turn.Sent || t.State == turn.Started {
			return "its turn is " + string(t.State), promptWindow{}, nil
		}
	}
	agents, err := s.Sessions.Agents(ctx)
	if err != nil {
		return "", promptWindow{}, err
	}
	busy := func(a AgentObservation) bool { return a.State == turn.AgentWorking || a.State == turn.AgentWaiting }
	agent, has := agentOf(agents, sess, Terminal{})
	switch {
	case has && agent.State == turn.AgentWorking:
		return "its agent is working", promptWindow{}, nil
	case has && agent.State == turn.AgentWaiting:
		return "its agent is waiting for permission", promptWindow{}, nil
	case sess.Pane == "" && slices.ContainsFunc(agents, func(a AgentObservation) bool { return a.WorkspaceID == sess.WorkspaceID && busy(a) }):
		return "an agent of its workspace is busy, and nothing tells it is not this session's", promptWindow{}, nil
	case slices.ContainsFunc(agents, func(a AgentObservation) bool { return a.UserActive && a.WorkspaceID == sess.WorkspaceID }):
		return "its workspace is the one open in the app", promptWindow{}, nil
	}
	w := windowOf(sess, agent, newest.startedAt)
	why, err := s.promptRefusal(ctx, w)
	return why, w, err
}
