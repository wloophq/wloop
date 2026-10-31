package turns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type SendRequest struct {
	Unit           unit.Unit
	Intent         turn.Intent
	Turn           string
	Trigger        turn.Trigger
	Guidance, Head string
	ResumeOf       int64
	// Inputs are the intent's inputs the turn is sent with.
	Inputs map[string]any
	// Agent is the agent of the pull request's project.
	Agent string
	// BriefRun is the brief run the turn's instructions carry, 0 for none.
	BriefRun int64
	// Records is false for a dry run and the take-over question: no turn.
	Records bool
	// Workflow is the workflow whose turn.start step started the turn, "" none.
	Workflow string
	// Role is the role the turn's intent holds, "" none.
	Role string
	// PerSession replaces, on a start by hand, only the open turns of the
	// intent on the starting session, as the role says.
	PerSession bool
	// KeepsAnswered refuses an automated send while the intent's open
	// question has an answer on its way, which the send would replace.
	KeepsAnswered bool
	// Items and Task are the unit's item ids and the task the turn was asked about, kept with
	// the turn so its drafts are tied to them.
	Items []string
	Task  string
}

// Send stores a new turn, replacing the open one of the same intent. The
// views learn it from SentTo, once it is typed.
func (s *Service) Send(ctx context.Context, r SendRequest) (turn.Turn, error) {
	if !r.Records {
		return turn.Turn{}, nil
	}
	return s.send(ctx, r, false, "")
}

// StartByHand stores a turn the user started in handle's session, "" when
// the session runs outside Orca.
func (s *Service) StartByHand(ctx context.Context, r SendRequest, handle string) (turn.Turn, error) {
	t, err := s.send(ctx, r, true, handle)
	if err != nil {
		return turn.Turn{}, err
	}
	s.claimSession(ctx, handle, r.Unit)
	s.changed(ctx, r.Unit)
	return t, nil
}

func (s *Service) send(ctx context.Context, r SendRequest, start bool, handle string) (turn.Turn, error) {
	if r.Turn == "" {
		return turn.Turn{}, errors.New("a turn needs an id")
	}
	s.mu.Lock()
	b, err := s.book(ctx, r.Unit)
	if err != nil {
		s.mu.Unlock()
		return turn.Turn{}, err
	}
	if err := refuseOverRunning(&b, r.Intent); err != nil {
		s.mu.Unlock()
		return turn.Turn{}, err
	}
	if r.KeepsAnswered && !start && r.ResumeOf == 0 && b.QuestionAnswered(r.Intent) {
		s.mu.Unlock()
		return turn.Turn{}, Refusal{fmt.Sprintf("%s: its %s question has an answer on its way", r.Unit.Label(), r.Intent)}
	}
	now := s.now()
	next := turn.Turn{
		Intent: r.Intent, ID: r.Turn, Trigger: r.Trigger, Guidance: r.Guidance, Head: r.Head,
		ResumeOf: r.ResumeOf, Inputs: bound(r.Inputs, r.Items, r.Task), SentAt: now, Agent: r.Agent, BriefRun: r.BriefRun, Workflow: r.Workflow, Role: r.Role,
	}
	if r.PerSession && start {
		b.SendOn(next, handle)
	} else {
		b.Send(next)
	}
	evs := []port.Event{s.event(r.Unit, KindTurnSent, r.Turn, string(r.Intent))}
	if start {
		t, err := b.Start(r.Turn, handle, now)
		if err != nil {
			s.mu.Unlock()
			return turn.Turn{}, err
		}
		t.AgentSession = s.agentSession(ctx, handle)
		evs = append(evs, s.event(r.Unit, KindTurnStarted, r.Turn, handle))
	}
	saved, err := s.Store.Save(ctx, b, evs...)
	s.mu.Unlock()
	if err != nil {
		return turn.Turn{}, err
	}
	s.publish(evs...)
	t, _ := saved.ByID(r.Turn)
	return *t, nil
}

// SentTo names the session the turn was typed into.
func (s *Service) SentTo(ctx context.Context, u unit.Unit, id, handle string) error {
	s.mu.Lock()
	b, err := s.book(ctx, u)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	t, ok := b.ByID(id)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%s has no turn %s", u.Label(), id)
	}
	if t.State != turn.Sent {
		s.mu.Unlock()
		return Refusal{fmt.Sprintf("turn %s is %s, not sent", id, t.State)}
	}
	t.SentTo = handle
	ev := s.event(u, KindTurnDelivered, id+":"+handle, "")
	_, err = s.Store.Save(ctx, b, ev)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	s.claimSession(ctx, handle, u)
	s.changed(ctx, u)
	return nil
}

// Typed notes the line the loop typed into the handle's session, so its
// prompt hook and transcript tell it from the user's.
func (s *Service) Typed(ctx context.Context, handle, line string) error {
	return s.updateSession(ctx, handle, func(cur *turn.Session, now time.Time) (string, string, string) {
		cur.TypedAt, cur.TypedLine = now, line
		return KindSessionTyped, handle + ":" + now.Format(time.RFC3339Nano), ""
	})
}

// Withdraw undoes the send of id's turn, one the loop failed to type: the
// turn is withdrawn and the turns it replaced are open again, as if it was
// never sent.
func (s *Service) Withdraw(ctx context.Context, u unit.Unit, id, why string) error {
	s.mu.Lock()
	b, err := s.book(ctx, u)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	t, ok := b.ByID(id)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%s has no turn %s", u.Label(), id)
	}
	if t, err = b.Withdraw(t.Seq, s.now()); errors.Is(err, turn.ErrClosed) {
		s.mu.Unlock()
		return Refusal{fmt.Sprintf("turn %s is %s, not sent", id, t.State)}
	} else if err != nil {
		s.mu.Unlock()
		return err
	}
	ev := s.event(u, KindTurnWithdrawn, id+":"+s.now().Format(time.RFC3339Nano), oneLine(why, answeredMax))
	_, err = s.Store.Save(ctx, b, ev)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	s.changed(ctx, u)
	return nil
}

// Capable says whether the loop may type a turn into the handle's session.
func (s *Service) Capable(ctx context.Context, handle string) (bool, error) {
	sess, ok, err := s.Registry.Session(ctx, handle)
	if err != nil || !ok {
		return false, err
	}
	return sess.ClosedAt.IsZero() && sess.Capable(), nil
}

// Abandoned marks closed a session the loop started for a round that never
// reached it, so no later round types into it.
func (s *Service) Abandoned(ctx context.Context, handle string) error {
	if handle == "" {
		return errors.New("an abandoned session needs a handle")
	}
	return s.saveSession(ctx, handle, func(cur *turn.Session, now time.Time) (string, string) {
		cur.Managed, cur.ClosedAt = true, now
		return KindSessionAbandoned, handle
	})
}

// Launched registers a session the loop started; its start time comes from the
// SessionStart hook.
func (s *Service) Launched(ctx context.Context, st turn.Session) error {
	if st.ID == "" {
		return errors.New("a launched session needs a handle")
	}
	err := s.saveSession(ctx, st.ID, func(cur *turn.Session, _ time.Time) (string, string) {
		cur.Managed = true
		if !st.Unit.IsZero() {
			cur.Unit = st.Unit
		}
		for _, f := range []struct {
			dst *string
			src string
		}{{&cur.WorkspaceID, st.WorkspaceID}, {&cur.Worktree, st.Worktree}, {&cur.Home, st.Home}, {&cur.Manager, st.Manager}, {&cur.Agent, st.Agent}} {
			if f.src != "" {
				*f.dst = f.src
			}
		}
		if cur.AgentSession == "" {
			cur.AgentSession = st.AgentSession
		}
		return KindSessionLaunched, st.ID
	})
	if err != nil || st.Manager == "" || st.Handle == "" {
		return err
	}
	return s.Bind(ctx, st.ID, st.Manager, st.Handle, "")
}

// Start marks the turn of id started on handle's session, the one an agent
// runs `instructions --turn` from.
func (s *Service) Start(ctx context.Context, handle, id string) (turn.Turn, error) {
	s.mu.Lock()
	b, ok, err := s.Store.BookOf(ctx, Key{Turn: id})
	if err != nil {
		s.mu.Unlock()
		return turn.Turn{}, err
	}
	before, found := b.ByID(id)
	if !ok || !found {
		s.mu.Unlock()
		return turn.Turn{}, Refusal{fmt.Sprintf("no turn %s", id)}
	}
	wasSent, sentTo := before.State == turn.Sent, before.SentTo
	t, err := b.Start(id, handle, s.now())
	switch {
	case errors.Is(err, turn.ErrClosed):
		s.mu.Unlock()
		return *t, Refusal{fmt.Sprintf("turn %s is %s", id, t.State)}
	case errors.Is(err, turn.ErrOtherSession):
		s.mu.Unlock()
		return *t, Refusal{fmt.Sprintf("turn %s is started on another session", id)}
	case err != nil:
		s.mu.Unlock()
		return turn.Turn{}, err
	case !wasSent:
		s.mu.Unlock()
		return *t, nil
	}
	t.AgentSession = s.agentSession(ctx, handle)
	data := handle
	if sentTo != "" && sentTo != handle {
		data = fmt.Sprintf("sent to %s, started on %s", sentTo, handle)
	}
	ev := s.event(b.Unit, KindTurnStarted, id, data)
	saved, err := s.Store.Save(ctx, b, ev)
	s.mu.Unlock()
	if err != nil {
		return turn.Turn{}, err
	}
	s.publish(ev)
	s.changed(ctx, b.Unit)
	started, _ := saved.ByID(id)
	return *started, nil
}
