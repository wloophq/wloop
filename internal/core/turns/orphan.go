package turns

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/domain/turn"
)

// RefusalOrphanUnproven refuses closing a process the census has not proven
// a durable orphan (see OrphanDurable).
const RefusalOrphanUnproven = "its process is not proven a lasting orphan"

const sessionDormant = "the session is dormant"

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
// allows it. It records nothing on the session: the censuses that then miss
// the process mark it dormant.
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
	switch {
	case CloseOrphanGone(id, why):
		return Gone{Reason: why}
	case why != "":
		return Refusal{Reason: why}
	}
	return s.Sessions.Close(ctx, id)
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
	case s.typingInto(id):
		return "its turn is " + string(turn.Sent), promptWindow{}, nil
	}
	newest, err := s.sessionTurns(ctx, id)
	if err != nil {
		return "", promptWindow{}, err
	}
	for _, t := range newest.open {
		if t.State == turn.Sent || t.State == turn.Started {
			return "its turn is " + string(t.State), promptWindow{}, nil
		}
	}
	if !s.OrphanDurable(id) {
		return RefusalOrphanUnproven, promptWindow{}, nil
	}
	agents, err := s.Sessions.Agents(ctx)
	if err != nil {
		return "", promptWindow{}, err
	}
	agent, has := agentOf(agents, sess, Terminal{})
	switch {
	case has && agent.State == turn.AgentWorking:
		return "its agent is working", promptWindow{}, nil
	case has && agent.State == turn.AgentWaiting:
		return "its agent is waiting for permission", promptWindow{}, nil
	case slices.ContainsFunc(agents, func(a AgentObservation) bool { return a.UserActive && a.WorkspaceID == sess.WorkspaceID }):
		return "its workspace is the one open in the app", promptWindow{}, nil
	}
	w := windowOf(sess, agent, newest.startedAt)
	why, err := s.promptRefusal(ctx, w)
	return why, w, err
}
