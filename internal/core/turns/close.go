package turns

import (
	"context"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// MarkDormant records that the session's process is gone: its manager no
// longer lists it. The session stays open, and its manager can wake it. It
// refuses a session the manager still lists in any state, one with no
// workspace, and a manager that cannot list completely.
func (s *Service) MarkDormant(ctx context.Context, id string) error {
	sess, ok, err := s.Registry.Session(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return Refusal{Reason: "no session " + id}
	}
	if sess.State() != turn.SessionActive {
		return nil
	}
	if sess.WorkspaceID == "" {
		return Refusal{Reason: "no workspace for " + id}
	}
	s.refresh()
	census, err := s.Sessions.Census(ctx)
	if err != nil {
		return err
	}
	if !census.CompleteOf(sess.Manager) {
		return Refusal{Reason: "its manager's process list is incomplete"}
	}
	if _, listed := census.Sessions[id]; listed {
		return Refusal{Reason: "the session's process is still listed"}
	}
	s.mu.Lock()
	cur, ok, err := s.Registry.Session(ctx, id)
	if err != nil || !ok || cur.State() != turn.SessionActive {
		s.mu.Unlock()
		return err
	}
	ev := s.event(unit.Unit{}, KindSessionDormant, id+":"+cur.Handle, "")
	if err = s.handles().EndHandle(ctx, id, s.now()); err == nil {
		err = s.Registry.SaveSession(ctx, withoutHandle(cur), ev)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	return nil
}

// withoutHandle is sess saved without binding its old handle again.
func withoutHandle(sess turn.Session) turn.Session {
	sess.Handle = ""
	return sess
}

// CloseRefusal is why session.close may not close the handle's session, ""
// when it may. It writes nothing; an error is a read that failed.
func (s *Service) CloseRefusal(ctx context.Context, handle string) (string, error) {
	why, _, err := s.closeCheck(ctx, handle)
	return why, err
}

func (s *Service) closeCheck(ctx context.Context, handle string) (string, promptWindow, error) {
	sess, ok, err := s.Registry.Session(ctx, handle)
	switch {
	case err != nil:
		return "", promptWindow{}, err
	case !ok:
		return "no session " + handle, promptWindow{}, nil
	case !sess.ClosedAt.IsZero():
		return "the session is closed", promptWindow{}, nil
	case s.typingInto(handle):
		return "its turn is " + string(turn.Sent), promptWindow{}, nil
	}
	newest, err := s.sessionTurns(ctx, handle)
	if err != nil {
		return "", promptWindow{}, err
	}
	for _, t := range newest.open {
		if t.State == turn.Sent || t.State == turn.Started {
			return "its turn is " + string(t.State), promptWindow{}, nil
		}
	}
	if sess.State() == turn.SessionDormant {
		return "", promptWindow{sess: sess}, nil
	}
	agent, has, focused, err := s.agentNow(ctx, sess)
	switch {
	case err != nil:
		return "", promptWindow{}, err
	case !has:
		return "no agent", promptWindow{}, nil
	case agent.State == turn.AgentWorking:
		return "its agent is working", promptWindow{}, nil
	case agent.State == turn.AgentWaiting:
		return "its agent is waiting for permission", promptWindow{}, nil
	case agent.State != turn.AgentDone:
		return "its agent is not done", promptWindow{}, nil
	case focused:
		return "its workspace is the one open in the app", promptWindow{}, nil
	}
	w := windowOf(sess, agent, newest.startedAt)
	why, err := s.promptRefusal(ctx, w)
	return why, w, err
}

// CloseSession closes the handle's session for reason when CloseRefusal
// allows it, and records session_closed with its resume line.
func (s *Service) CloseSession(ctx context.Context, handle, reason string) error {
	s.refresh()
	why, w, err := s.closeCheck(ctx, handle)
	if err != nil {
		return err
	}
	if why == "" && !w.sess.PromptSeenAt.IsZero() {
		at, ok, _ := s.lastUserPrompt(ctx, w.sess)
		why = w.transcriptRefusal(at, ok)
	}
	if why != "" {
		return Refusal{Reason: why}
	}
	units, err := s.Store.UnitsOf(ctx, []string{handle})
	if err != nil {
		return err
	}
	if err := s.Sessions.Close(ctx, handle); err != nil {
		return err
	}
	s.mu.Lock()
	cur, ok, err := s.Registry.Session(ctx, handle)
	if err != nil || !ok || !cur.ClosedAt.IsZero() {
		s.mu.Unlock()
		return err
	}
	cur.ClosedAt = s.now()
	ev := s.event(unit.Unit{}, KindSessionClosed, handle, s.closeDetail(sessionFacts{Session: cur, Unit: units[handle]}, reason))
	if cur.Handle != "" {
		err = s.handles().EndHandle(ctx, handle, cur.ClosedAt)
	}
	if err == nil {
		err = s.Registry.SaveSession(ctx, withoutHandle(cur), ev)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	return nil
}

// handleTurns are the open turns sent to or started on a handle, with the
// sent ones of its book still being typed, and the latest start among all of
// its turns.
type handleTurns struct {
	open      []turn.Turn
	startedAt time.Time
}

func (s *Service) sessionTurns(ctx context.Context, handle string) (handleTurns, error) {
	var out handleTurns
	b, ok, err := s.Store.BookOf(ctx, Key{Session: handle})
	if err != nil || !ok {
		return out, err
	}
	for _, t := range b.Turns {
		if headless(t) {
			continue
		}
		if t.State == turn.Sent && t.SentTo == "" {
			out.open = append(out.open, t)
			continue
		}
		if t.Session != handle && !(t.State == turn.Sent && t.SentTo == handle) {
			continue
		}
		if t.State.Open() {
			out.open = append(out.open, t)
		}
		if t.StartedAt.After(out.startedAt) {
			out.startedAt = t.StartedAt
		}
	}
	return out, nil
}

// agentNow is the session's agent as the manager lists it, and whether its
// workspace is the one open in the app.
func (s *Service) agentNow(ctx context.Context, sess turn.Session) (a AgentObservation, has, focused bool, err error) {
	if sess.WorkspaceID == "" {
		return AgentObservation{}, false, false, nil
	}
	agents, err := s.Sessions.Agents(ctx)
	if err != nil {
		return AgentObservation{}, false, false, err
	}
	terms, err := s.Sessions.Terminals(ctx, sess.WorkspaceID)
	if err != nil {
		return AgentObservation{}, false, false, err
	}
	i := slices.IndexFunc(terms, func(t Terminal) bool { return t.Session == sess.ID })
	if i < 0 {
		return AgentObservation{}, false, false, nil
	}
	a, has = agentOf(agents, sess, terms[i])
	focused = slices.ContainsFunc(agents, func(a AgentObservation) bool { return a.UserActive && a.WorkspaceID == sess.WorkspaceID })
	return a, has, focused, nil
}

const cannotTell = "cannot tell whether you typed in it"

// promptWindow is what a prompt must come after to refuse a close: the
// loop's last turn in a managed session, the idle period's start in a manual
// one. The newest turn's start counts as the loop's last turn too: a session
// typed into before TypedAt existed shows the loop's own prompt in its
// transcript.
type promptWindow struct {
	sess  turn.Session
	since time.Time
	typed string
}

func windowOf(sess turn.Session, agent AgentObservation, startedAt time.Time) promptWindow {
	w := promptWindow{sess: sess, since: sess.IdleSince, typed: "you prompted it while it was idle"}
	if !agent.StateSince.IsZero() {
		w.since = agent.StateSince
	}
	if sess.Managed {
		w.since, w.typed = sess.TypedAt, "you typed in it after "+brand.Name+"'s last turn"
		if startedAt.After(w.since) {
			w.since = startedAt
		}
	}
	return w
}

// promptRefusal reads the prompt hook's times, else the transcript, and
// refuses when neither is there.
func (s *Service) promptRefusal(ctx context.Context, w promptWindow) (string, error) {
	if !w.sess.PromptSeenAt.IsZero() {
		if w.sess.UserPromptAt.After(w.since) {
			return w.typed, nil
		}
		return "", nil
	}
	at, ok, err := s.lastUserPrompt(ctx, w.sess)
	if err != nil || !ok {
		return cannotTell, err
	}
	return w.transcriptRefusal(at, ok), nil
}

// transcriptRefusal refuses on a readable transcript prompted after the
// window's start; an unreadable one refuses nothing.
func (w promptWindow) transcriptRefusal(at time.Time, ok bool) string {
	if ok && at.After(w.since) {
		return w.typed
	}
	return ""
}

// lastUserPrompt logs a failed read and treats it as an unreadable transcript.
func (s *Service) lastUserPrompt(ctx context.Context, sess turn.Session) (time.Time, bool, error) {
	if s.Prompts == nil {
		return time.Time{}, false, nil
	}
	at, ok, err := s.Prompts.LastUserPrompt(ctx, sess)
	if err != nil {
		s.logf("reading the transcript of %s: %v", sess.ID, err)
		return time.Time{}, false, nil
	}
	return at, ok, nil
}
