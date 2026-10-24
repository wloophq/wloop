package turns

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
)

// KeepSessions registers the agent terminals that carry the loop's titles
// and keeps the idle period of every open managed session.
func (s *Service) KeepSessions(ctx context.Context) error {
	if s.Intents == nil {
		return ErrNoIntents
	}
	o, err := s.lookAround(ctx)
	if err != nil {
		return err
	}
	sessions, err := s.Registry.Sessions(ctx)
	if err != nil {
		return err
	}
	registered, err := s.backfill(ctx, o, sessions)
	if err != nil {
		return err
	}
	for _, sess := range append(sessions, registered...) {
		if !sess.Managed {
			continue
		}
		if _, _, err := s.look(ctx, o, sess); err != nil {
			return err
		}
	}
	return nil
}

// AgentOf is the agent running in the session's terminal, matched as cleanup
// and session.close match them.
func AgentOf(agents []AgentObservation, sess turn.Session, term Terminal) (AgentObservation, bool) {
	return agentOf(agents, sess, term)
}

// PromptFacts are what session.close's prompt guard reads, as facts.
type PromptFacts struct {
	// Knowable is false when neither the prompt hook nor a readable
	// transcript tells when a person last prompted the session.
	Knowable bool
	// TypedAfterTurn is a managed session prompted after the loop's last turn.
	TypedAfterTurn bool
	// PromptedWhileIdle is a session prompted after its idle period began.
	PromptedWhileIdle bool
}

// PromptFacts reads the session's prompt facts with the windows CloseRefusal
// uses; agent is the session's agent, zero when it has none. It writes nothing.
func (s *Service) PromptFacts(ctx context.Context, sess turn.Session, agent AgentObservation) (PromptFacts, error) {
	ht, err := s.sessionTurns(ctx, sess.ID)
	if err != nil {
		return PromptFacts{}, err
	}
	at, known := sess.UserPromptAt, !sess.PromptSeenAt.IsZero()
	if !known {
		at, known, _ = s.lastUserPrompt(ctx, sess)
	}
	var f PromptFacts
	if !known {
		return f, nil
	}
	f.Knowable = true
	if sess.Managed {
		f.TypedAfterTurn = at.After(windowOf(sess, agent, ht.startedAt).since)
	}
	idle := sess
	idle.Managed = false
	if agent.State != turn.AgentDone {
		agent.StateSince = time.Time{}
	}
	if since := windowOf(idle, agent, time.Time{}).since; !since.IsZero() {
		f.PromptedWhileIdle = at.After(since)
	}
	return f, nil
}

// NextGone is the earliest time a census mark can make a session gone, zero
// when no session is marked; a session already gone has no deadline left.
func (s *Service) NextGone() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next time.Time
	for h, m := range s.goneSeen {
		if s.gone[h] {
			continue
		}
		if at := m.at.Add(s.FastJob); next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return next
}
