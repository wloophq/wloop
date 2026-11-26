package gates

import (
	"cmp"
	"context"
	"errors"
	"fmt"

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

// SupersedeQuestion closes open question gate id as superseded by the workflow when gone, run under the question's
// claim so no pick decides it meanwhile, says its subject is gone; it reports whether it closed the gate. A question
// being decided, or one no longer open, is left as it is.
func (s *Service) SupersedeQuestion(ctx context.Context, id int64, gone func(context.Context) (bool, error)) (bool, error) {
	release, ok := s.claimQuestion(id)
	if !ok {
		return false, nil
	}
	defer release()
	g, err := s.d.Reader.Get(ctx, id)
	switch {
	case err != nil:
		return false, err
	case g.Kind != gate.KindQuestion:
		return false, fmt.Errorf("%w: gate %d is a %s gate, not a question", ErrRefused, id, g.Kind)
	case g.Status != gate.Open:
		return false, nil
	}
	if ok, err := gone(ctx); err != nil || !ok {
		return false, err
	}
	if _, err := s.d.Store.Resolve(ctx, id, Resolution{Status: gate.Superseded, By: task.ActorWorkflow}); err != nil {
		if errors.Is(err, ErrResolved) {
			return false, nil
		}
		return false, err
	}
	return true, nil
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

// ApproveOption approves draft gate drafted, which the option a person (or an agent through a session
// the user drives) picked points to at rev, the rev the option pinned, then resolves question gate
// question with r, the option picked (Intent, Answer) and who picked it (By, Session). It reads both gates
// again from the store first, trusting neither value the caller passes, and resolves nothing when drafted
// is no longer open (or approved already by r's resolver) at rev or is on another unit than the question, or
// when r is a rule's: each is ErrRefused. The caller ties drafted to the question: its Options name only a draft
// of the question's turn chain. The draft is approved before the question is answered, so an answered question names the
// one draft its answer approved: a later call on an answered question completes only with that draft gate
// approved, and refuses any other draft gate, another option's or a revision's, with ErrRefused. A draft
// that loses its approval to a race (a revision) leaves the question open. Each step is idempotent, so a
// call cut short completes when it is made again with the same r.
func (s *Service) ApproveOption(ctx context.Context, question gate.Gate, r Resolution, drafted gate.Gate, rev int64) error {
	return s.approveOption(ctx, question, r, drafted, rev, nil)
}

// Edit is a person's change of an option's draft, made as they pick the option: it revises the draft drafted
// holds, which supersedes drafted, and returns the open gate the revision waits on.
type Edit func(ctx context.Context, drafted gate.Gate) (gate.Gate, error)

// PickOption answers open question gate id with r, which picks an option pinning a held draft, approving that
// draft at rev, the rev the person saw, which may be newer than the one the option pinned, after edit (nil for
// none) changes it, all under the question's claim, as ApproveOption does. It refuses with ErrRefused an answer
// that picks no held draft, and a rev the draft no longer waits on: the person saw another version of it.
func (s *Service) PickOption(ctx context.Context, id int64, r Resolution, rev int64, edit Edit) error {
	if s.d.Options == nil {
		return fmt.Errorf("%w: question gate %d picks no draft here", ErrRefused, id)
	}
	q, err := s.d.Reader.Get(ctx, id)
	switch {
	case err != nil:
		return err
	case q.Kind != gate.KindQuestion:
		return fmt.Errorf("%w: gate %d is a %s gate, not a question", ErrRefused, id, q.Kind)
	case q.Status != gate.Open:
		return fmt.Errorf("%w: %d is %s", ErrResolved, id, q.Status)
	}
	drafted, _, ok, err := s.d.Options.OptionDraft(ctx, q, r)
	switch {
	case err != nil:
		return err
	case !ok:
		return fmt.Errorf("%w: the answer to question gate %d picks no draft waiting on the user: its option pins none, or its draft was discarded, withdrawn or posted since", ErrRefused, id)
	}
	return s.approveOption(ctx, q, r, drafted, rev, edit)
}

func (s *Service) approveOption(ctx context.Context, question gate.Gate, r Resolution, drafted gate.Gate, rev int64, edit Edit) error {
	release, ok := s.claimQuestion(question.ID)
	if !ok {
		return fmt.Errorf("%w: question gate %d is being decided", ErrResolved, question.ID)
	}
	defer release()
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
	if q.Status != gate.Open {
		return s.answered(q, d, r)
	}
	if d.Status == gate.Approved && (d.Rule != "" || d.By != r.By || d.BySession != sessionOf(r)) {
		return fmt.Errorf("%w: the draft of gate %d was approved otherwise than by this answer", ErrRefused, d.ID)
	}
	if err := s.answers(ctx, q, r); err != nil {
		return err
	}
	if edit != nil && d.Status == gate.Open {
		if d, err = edit(ctx, d); err != nil {
			return err
		}
		if err := s.pinned(q, d, r, d.ApprovedRev); err != nil {
			return err
		}
	}
	if d.Status == gate.Open {
		if err := s.approveDraft(ctx, d, r); err != nil {
			return err
		}
	}
	if _, err := s.resolve(ctx, q.ID, r, s.d.Clock.Now()); err != nil && !errors.Is(err, ErrResolved) {
		return err
	}
	if q, err = s.d.Reader.Get(ctx, q.ID); err != nil {
		return err
	}
	if d, err = s.d.Reader.Get(ctx, d.ID); err != nil {
		return err
	}
	return s.answered(q, d, r)
}

// answers refuses r unless it answers open question q: an intent q offers whose status is answered and
// that takes the answer r carries, from a resolver q accepts. It runs before the draft is approved, so a
// resolution the question would refuse approves nothing.
func (s *Service) answers(ctx context.Context, q gate.Gate, r Resolution) error {
	def, err := s.def(q)
	if err != nil {
		return err
	}
	in, err := withStatus(q, def, r)
	if err != nil {
		return err
	}
	if in.Status != gate.Answered || r.Answer == "" {
		return fmt.Errorf("%w: intent %q of gate %d does not answer it, so it picks no option", ErrRefused, r.Intent, q.ID)
	}
	if r.By == task.ActorAgent {
		return s.vouch(ctx, r.Session)
	}
	return nil
}

// answered refuses question q, resolved, unless r resolved it and its draft gate d is approved: the
// option's draft is approved before its question is answered, so an open draft gate is not the answer's.
func (s *Service) answered(q, d gate.Gate, r Resolution) error {
	if q.Intent != r.Intent || q.Answer != r.Answer || q.By != r.By {
		return fmt.Errorf("%w: question gate %d was resolved otherwise", ErrResolved, q.ID)
	}
	if d.Status != gate.Approved {
		return fmt.Errorf("%w: question gate %d was answered without draft gate %d, which its answer does not approve", ErrRefused, q.ID, d.ID)
	}
	return nil
}

// approveDraft approves open draft gate d with r's resolver; one it approved already counts.
func (s *Service) approveDraft(ctx context.Context, d gate.Gate, r Resolution) error {
	def, err := s.def(d)
	if err != nil {
		return err
	}
	approve, ok := statusIntent(d, def, gate.Approved)
	if !ok {
		return fmt.Errorf("%w: draft gate %d has no approving intent", ErrIntent, d.ID)
	}
	_, err = s.resolve(ctx, d.ID, Resolution{Intent: approve.Name, By: r.By, Session: r.Session, pick: true}, s.d.Clock.Now())
	if errors.Is(err, ErrResolved) {
		if got, gerr := s.d.Reader.Get(ctx, d.ID); gerr == nil && got.Status == gate.Approved && got.By == r.By && got.BySession == sessionOf(r) {
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
		return fmt.Errorf("%w: %w: rev %d of the draft of gate %d was picked, and it waits on rev %d: read it again and pick again", ErrRefused, ErrMoved, rev, d.ID, d.ApprovedRev)
	case d.Unit != q.Unit:
		return fmt.Errorf("%w: the draft of gate %d is on %s, not on the question's %s", ErrRefused, d.ID, d.Unit, q.Unit)
	case r.By != task.ActorPerson && r.By != task.ActorAgent:
		return fmt.Errorf("%w: an option is picked by a person or a session the user drives, not by %s", ErrRefused, r.By)
	}
	return nil
}

func sessionOf(r Resolution) string {
	if r.By == task.ActorPerson {
		return ""
	}
	return r.Session
}
