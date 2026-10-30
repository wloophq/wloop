package turns

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Run is one turn on a task as its timeline shows it: the workspace manager,
// workspace path and worktree it ran in, its agent session, the session's
// live state and the command that resumes it. A headless call has no
// Manager, Workspace or Session.
type Run struct {
	Turn          turn.Turn
	Manager       string
	Workspace     string
	Worktree      string
	Session       string
	State         string
	Resume        string
	HasTranscript bool
}

// TranscriptLine is one message of an agent session's transcript.
type TranscriptLine struct {
	Role string
	Text string
	At   time.Time
}

// Transcripts pages an agent session's transcript.
type Transcripts interface {
	// Page is at most limit lines from line from (0-based), oldest first,
	// and the transcript's line count.
	Page(ctx context.Context, agentSession string, from, limit int) ([]TranscriptLine, int, error)
}

// TurnUnits is a Store that finds the unit of a turn by its seq without
// reading every book.
type TurnUnits interface {
	UnitOfTurn(ctx context.Context, seq int64) (unit.Unit, bool, error)
}

// RunsOf are the runs of u, newest first: its turns but the withdrawn and
// replaced ones, each with the session it ran in. A run's Turn.AgentSession
// is the turn's agent session, else its session's.
func (s *Service) RunsOf(ctx context.Context, u unit.Unit) ([]Run, error) {
	b, err := s.book(ctx, u)
	if err != nil {
		return nil, err
	}
	out := []Run{}
	for i := len(b.Turns) - 1; i >= 0; i-- {
		t := b.Turns[i]
		if t.State == turn.Withdrawn || t.State == turn.Replaced {
			continue
		}
		r, err := s.run(ctx, u, t)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// RunOf is the run of turn seq and its unit; false when no book holds it.
func (s *Service) RunOf(ctx context.Context, seq int64) (Run, unit.Unit, bool, error) {
	u, ok, err := s.unitOfTurn(ctx, seq)
	if err != nil || !ok {
		return Run{}, unit.Unit{}, false, err
	}
	b, err := s.book(ctx, u)
	if err != nil {
		return Run{}, unit.Unit{}, false, err
	}
	t, ok := bySeq(&b, seq)
	if !ok {
		return Run{}, unit.Unit{}, false, nil
	}
	r, err := s.run(ctx, u, *t)
	return r, u, err == nil, err
}

func (s *Service) unitOfTurn(ctx context.Context, seq int64) (unit.Unit, bool, error) {
	if tu, ok := s.Store.(TurnUnits); ok {
		return tu.UnitOfTurn(ctx, seq)
	}
	books, err := s.Store.Books(ctx)
	if err != nil {
		return unit.Unit{}, false, err
	}
	for u, b := range books {
		if _, ok := bySeq(&b, seq); ok {
			return u, true, nil
		}
	}
	return unit.Unit{}, false, nil
}

func (s *Service) run(ctx context.Context, u unit.Unit, t turn.Turn) (Run, error) {
	r := Run{Turn: t}
	id := cmp.Or(t.Session, t.SentTo)
	if id == "" {
		return r, nil
	}
	sess, ok, err := s.Registry.Session(ctx, id)
	if err != nil || !ok {
		return r, err
	}
	r.Manager, r.Workspace, r.Worktree, r.Session, r.State = sess.Manager, sess.WorkspaceID, sess.Worktree, sess.ID, string(sess.State())
	r.Turn.AgentSession = cmp.Or(t.AgentSession, sess.AgentSession)
	r.HasTranscript = r.Turn.AgentSession != ""
	if r.HasTranscript && s.AgentFor != nil {
		if a := s.AgentFor(u); a != nil {
			r.Resume = a.Resume(sess.Home, r.Turn.AgentSession)
		}
	}
	return r, nil
}

// ClaimFor makes u the unit of the session id whatever unit it had, for a
// session that attaches to u.
func (s *Service) ClaimFor(ctx context.Context, id string, u unit.Unit) error {
	if id == "" || u.IsZero() {
		return Refusal{"attaching needs the session and the unit it attaches to"}
	}
	s.mu.Lock()
	cur, ok, err := s.Registry.Session(ctx, id)
	if err != nil || !ok {
		s.mu.Unlock()
		return cmp.Or(err, error(Refusal{fmt.Sprintf("no session %s", id)}))
	}
	if cur.Unit == u {
		s.mu.Unlock()
		return nil
	}
	cur.Unit = u
	ev := s.event(unit.Unit{}, KindSessionUnit, id, u.String())
	err = s.Registry.SaveSession(ctx, cur, ev)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	s.changed(ctx, u)
	return nil
}
