package turns

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Chats are the messages the user wrote on a task's chat that no notice told an agent of yet.
type Chats interface {
	// Waiting is the task whose chat holds such messages, of the tasks unit u is; false when none does.
	Waiting(ctx context.Context, u unit.Unit) (task unit.Unit, ok bool, err error)
	// Noticed notes that an agent was told of every message on the chat of the task whose own unit is task.
	Noticed(ctx context.Context, task unit.Unit) error
}

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

// Busy says whether sess's agent is working or waiting on a permission, as an answer's route reads it: a
// chat to it waits for its next stop.
func (s *Service) Busy(ctx context.Context, sess turn.Session) (bool, error) {
	if sess.WorkspaceID == "" {
		return false, nil
	}
	s.refresh()
	agents, err := s.Sessions.Agents(ctx)
	if err != nil {
		return false, nil
	}
	return busy(sess, agents, turn.AgentWorking, turn.AgentWaiting), nil
}

// TellChat types into live session sess the notice that the user wrote on the chat of the task whose own
// unit is task, which carries no message: the agent reads it with a tool, or with the command when the
// session has not loaded the loop's MCP server. It is false when the agent took the line without submitting it.
func (s *Service) TellChat(ctx context.Context, sess turn.Session, task unit.Unit) (bool, error) {
	line := s.chatNotice(task, sess.Capable())
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

func (s *Service) chatNotice(task unit.Unit, capable bool) string {
	return s.notice(NoticeChat, map[string]any{"Task": task.Key, "Unit": unitArg(task), "Capable": capable})
}

// chatBlock is the stop of a session on unit u blocked with the notice of a chat no agent was told of, which
// it then counts as told; false when none waits.
func (s *Service) chatBlock(ctx context.Context, u unit.Unit, session string) (StopAnswer, bool) {
	if s.Chats == nil {
		return StopAnswer{}, false
	}
	task, ok, err := s.Chats.Waiting(ctx, u)
	if err != nil || !ok {
		if err != nil {
			s.logf("the chat waiting for %s: %v", session, err)
		}
		return StopAnswer{}, false
	}
	sess, _, err := s.Registry.Session(ctx, session)
	if err != nil {
		s.logf("the chat waiting for %s: %v", session, err)
	}
	if err := s.Chats.Noticed(ctx, task); err != nil {
		s.logf("noting the chat of %s told: %v", task.Key, err)
	}
	return StopAnswer{Block: true, Reason: s.chatNotice(task, sess.Capable())}, true
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
