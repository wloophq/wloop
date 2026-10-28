package turns

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// LiveOn is the newest started session with a live process whose unit is
// one of units; false when none is.
func (s *Service) LiveOn(ctx context.Context, units ...unit.Unit) (turn.Session, bool, error) {
	all, err := s.Registry.Sessions(ctx)
	if err != nil {
		return turn.Session{}, false, err
	}
	var out turn.Session
	found := false
	for _, sess := range all {
		if sess.State() != turn.SessionActive || !slices.Contains(units, sess.Unit) {
			continue
		}
		if !found || sess.StartedAt.After(out.StartedAt) {
			out, found = sess, true
		}
	}
	return out, found, nil
}

// TellChat types into live session sess the notice that the user wrote on
// task id's chat, which carries no message: the agent reads it with a tool.
// It is false when the agent took the line without submitting it.
func (s *Service) TellChat(ctx context.Context, sess turn.Session, id string) (bool, error) {
	line := s.notice(NoticeChat, map[string]any{"Task": id})
	typed := s.Typing(sess.ID)
	defer typed()
	submitted, err := s.Sessions.Type(ctx, sess.ID, line)
	if err != nil {
		return false, err
	}
	if terr := s.Typed(ctx, sess.ID, line); terr != nil {
		s.logf("noting the chat notice typed into %s: %v", sess.ID, terr)
	}
	return submitted, nil
}

// LastRun is u's newest turn that ran in a session, which a new turn can
// resume; false when none did.
func (s *Service) LastRun(ctx context.Context, u unit.Unit) (turn.Turn, bool, error) {
	b, err := s.book(ctx, u)
	if err != nil {
		return turn.Turn{}, false, err
	}
	for i := len(b.Turns) - 1; i >= 0; i-- {
		t := b.Turns[i]
		if t.State == turn.Withdrawn || t.State == turn.Replaced || t.Session == "" {
			continue
		}
		return t, true, nil
	}
	return turn.Turn{}, false, nil
}

// Launch runs l through the Launcher.
func (s *Service) Launch(ctx context.Context, l Launch) error {
	return s.Launcher.Launch(ctx, l)
}
