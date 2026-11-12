package gates

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// SupersedeForAgent closes open draft gate id as superseded because session, an agent, revised its draft,
// and returns the closed gate. It accepts any non-empty session: the outbox decides which agent may revise
// a draft, and stamps the closed gate's Sessions plus session on the gate it opens for the new rev. It runs
// no consequence, as Supersede does, and refuses every gate but a draft's.
func (s *Service) SupersedeForAgent(ctx context.Context, id int64, session string) (gate.Gate, error) {
	if session == "" {
		return gate.Gate{}, fmt.Errorf("%w: gate %d is superseded by an agent's revision only through its session", ErrRefused, id)
	}
	g, err := s.d.Reader.Get(ctx, id)
	if err != nil {
		return gate.Gate{}, err
	}
	switch {
	case g.Kind != gate.KindDraft:
		return gate.Gate{}, fmt.Errorf("%w: gate %d is a %s gate; an agent's revision supersedes a draft's only", ErrRefused, id, g.Kind)
	case g.Status != gate.Open:
		return g, fmt.Errorf("%w: %d is %s", ErrResolved, id, g.Status)
	}
	return s.d.Store.Resolve(ctx, id, Resolution{Status: gate.Superseded, By: task.ActorAgent, Session: session})
}

// TaskOf is the task gate g is placed on when it opens: its own when it names one, else the one Open picks,
// which may be a capture task opened now.
func (s *Service) TaskOf(ctx context.Context, g gate.Gate) (task.ID, error) {
	if g.Task != "" {
		return g.Task, nil
	}
	def, err := s.def(g)
	if err != nil {
		return "", err
	}
	return s.taskOf(ctx, g.Unit, cmp.Or(g.Text, def.Label))
}

// ApproveOption resolves question gate question with r, the option picked (Intent, Answer) and who picked
// it (By, Session: a person, or an agent through a session the user drives), when that option points to
// drafted's draft at rev; then it approves drafted with the same resolver. It reads both gates again from
// the store first, trusting neither value the caller passes, and resolves nothing when drafted is no
// longer open (or approved already) at rev, is on another unit than the question, came from another
// session than the question's, or stamps r.Session among its Sessions, or when r is a rule's: each is
// ErrRefused. When the draft's approval loses a race after the question resolved, as a revision landing
// between them, it returns that error, and the question's answer stands as a free answer. Each step is
// idempotent, so a call cut short completes when it is made again with the same r.
func (s *Service) ApproveOption(ctx context.Context, question gate.Gate, r Resolution, drafted gate.Gate, rev int64) error {
	q, err := s.d.Reader.Get(ctx, question.ID)
	if err != nil {
		return err
	}
	d, err := s.d.Reader.Get(ctx, drafted.ID)
	if err != nil {
		return err
	}
	if err := s.pinned(q, d, r, rev); err != nil {
		return err
	}
	if q.Status == gate.Open {
		if _, err := s.resolve(ctx, q.ID, r, s.d.Clock.Now()); err != nil && !errors.Is(err, ErrResolved) {
			return err
		}
		if q, err = s.d.Reader.Get(ctx, q.ID); err != nil {
			return err
		}
	}
	if q.Intent != r.Intent || q.Answer != r.Answer || q.By != r.By {
		return fmt.Errorf("%w: question gate %d was resolved otherwise", ErrResolved, q.ID)
	}
	if d.Status == gate.Approved {
		return nil
	}
	def, err := s.def(d)
	if err != nil {
		return err
	}
	approve, ok := statusIntent(d, def, gate.Approved)
	if !ok {
		return fmt.Errorf("%w: draft gate %d has no approving intent", ErrIntent, d.ID)
	}
	_, err = s.resolve(ctx, d.ID, Resolution{Intent: approve.Name, By: r.By, Session: r.Session}, s.d.Clock.Now())
	if errors.Is(err, ErrResolved) {
		if d, gerr := s.d.Reader.Get(ctx, d.ID); gerr == nil && d.Status == gate.Approved && d.By == r.By && d.BySession == sessionOf(r) {
			return nil
		}
	}
	return err
}

// pinned refuses an option of question q that points to draft gate d at rev, for resolver r.
func (s *Service) pinned(q, d gate.Gate, r Resolution, rev int64) error {
	switch {
	case q.Kind != gate.KindQuestion:
		return fmt.Errorf("%w: gate %d is a %s gate, not a question", ErrRefused, q.ID, q.Kind)
	case d.Kind != gate.KindDraft:
		return fmt.Errorf("%w: gate %d is a %s gate, not a draft's", ErrRefused, d.ID, d.Kind)
	case d.Status != gate.Open && d.Status != gate.Approved:
		return fmt.Errorf("%w: the draft of gate %d was revised or settled since the option pointed to it", ErrRefused, d.ID)
	case rev < 1 || d.ApprovedRev != rev:
		return fmt.Errorf("%w: the option points to rev %d of the draft of gate %d, which waits on rev %d", ErrRefused, rev, d.ID, d.ApprovedRev)
	case d.Unit != q.Unit:
		return fmt.Errorf("%w: the draft of gate %d is on %s, not on the question's %s", ErrRefused, d.ID, d.Unit, q.Unit)
	case q.Session != "" && d.Session != q.Session:
		return fmt.Errorf("%w: the draft of gate %d is not of the session that asked", ErrRefused, d.ID)
	case r.By != task.ActorPerson && r.By != task.ActorAgent:
		return fmt.Errorf("%w: an option is picked by a person or a session the user drives, not by %s", ErrRefused, r.By)
	case r.Session != "" && stamped(d, r.Session):
		return fmt.Errorf("%w: session %s proposed or revised the draft of gate %d", ErrRefused, r.Session, d.ID)
	}
	return nil
}

func sessionOf(r Resolution) string {
	if r.By == task.ActorPerson {
		return ""
	}
	return r.Session
}

// stamped says whether session proposed or revised the subject of g.
func stamped(g gate.Gate, session string) bool {
	return session != "" && (g.Session == session || slices.Contains(g.Sessions, session))
}
