package messages

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
)

// ReasonOtherOption is why a draft another option pointed to is withdrawn once one option's draft is approved.
const ReasonOtherOption = "other_option"

// OptionDrafts are the drafts a question's options point to, as the outbox and the gates service keep them.
type OptionDrafts interface {
	// DraftGate is draft id and the newest gate it waits or waited on; false when there is no such draft or gate.
	DraftGate(ctx context.Context, id int64) (draft.Draft, gate.Gate, bool, error)
	// WithdrawHeld withdraws draft id while it is still held, with reason; one no longer held is left as it is.
	WithdrawHeld(ctx context.Context, id int64, reason string) error
}

// OptionDraft is the draft gate r's answer to question gate question approves, and the rev its option
// pinned, which the gates service approves through ApproveOption before it answers the question: the
// draft the option, or a confirm's yes, points to in the message the person saw, held by that message's
// turn on its unit at the pinned rev, for a resolver that is a person or a session the user drives that is
// neither the turn's nor one that proposed or revised the draft. False for any other answer, which then
// resolves the question alone and resumes the turn.
func (s *Service) OptionDraft(ctx context.Context, question gate.Gate, r gates.Resolution) (gate.Gate, int64, bool, error) {
	if s.Drafts == nil || question.Kind != gate.KindQuestion || r.Rule != "" {
		return gate.Gate{}, 0, false, nil
	}
	switch {
	case r.By == task.ActorPerson:
		r.Session = ""
	case r.By != task.ActorAgent || r.Session == "" || r.Session == question.Session:
		return gate.Gate{}, 0, false, nil
	}
	m, err := s.subject(ctx, question)
	if errors.Is(err, ErrNoMessage) || errors.Is(err, ErrClosed) {
		return gate.Gate{}, 0, false, nil
	}
	if err != nil || m.Kind != message.Question {
		return gate.Gate{}, 0, false, err
	}
	ref, ok := pickedDraft(m, AnswerOf(m, r.Answer))
	if !ok {
		return gate.Gate{}, 0, false, nil
	}
	d, dg, ok, err := s.ofTurn(ctx, m, ref.ID)
	switch {
	case err != nil:
		return gate.Gate{}, 0, false, err
	case !ok || turns.ProposedOrRevised(dg, d, r.Session):
		s.logf("messages: message %d points to draft %d, which this answer does not approve; the turn resumes", m.ID, ref.ID)
		return gate.Gate{}, 0, false, nil
	case dg.Status != gate.Open || d.State != draft.Held || ref.Rev != int64(d.Rev) || ref.Rev != dg.ApprovedRev:
		s.logf("messages: draft %d is not held at the rev %d message %d pinned; the turn resumes", ref.ID, ref.Rev, m.ID)
		return gate.Gate{}, 0, false, nil
	}
	return dg, ref.Rev, true, nil
}

// approvedOption says whether g, m's answered gate, approved the draft the answer a picks, as
// OptionDraft named it and the gates service approved it: the draft gate approved at the pinned rev by g's
// own resolver, a person or a session the user drives that neither proposed nor revised it. It then
// withdraws the drafts the other options point to, so the answer ends the turn on the draft's send instead
// of resuming it; a call made again completes what is left.
func (s *Service) approvedOption(ctx context.Context, g gate.Gate, m message.Message, a Answer) (bool, error) {
	if s.Drafts == nil || m.Kind != message.Question {
		return false, nil
	}
	ref, ok := pickedDraft(m, a)
	if !ok || g.Kind != gate.KindQuestion || g.Status != gate.Answered || !g.ByUser() || g.BySession != "" && g.BySession == g.Session {
		return false, nil
	}
	d, dg, ok, err := s.ofTurn(ctx, m, ref.ID)
	switch {
	case err != nil:
		return false, err
	case !ok || turns.ProposedOrRevised(dg, d, g.BySession):
		return false, nil
	case dg.Status != gate.Approved || dg.ApprovedRev != ref.Rev || dg.By != g.By || dg.BySession != g.BySession || dg.Rule != "":
		return false, nil
	}
	for _, o := range m.Options {
		if o.Draft == nil || o.Draft.ID == ref.ID {
			continue
		}
		if _, _, ok, err := s.ofTurn(ctx, m, o.Draft.ID); err != nil || !ok {
			continue
		}
		if err := s.Drafts.WithdrawHeld(ctx, o.Draft.ID, ReasonOtherOption); err != nil {
			s.logf("messages: withdrawing draft %d of option %s of message %d: %v", o.Draft.ID, o.ID, m.ID, err)
		}
	}
	return true, nil
}

// pickedDraft is the draft a points to on m, as m pinned it when the person saw it: its option's, or the
// confirm's on a yes.
func pickedDraft(m message.Message, a Answer) (record.DraftRef, bool) {
	if m.Confirm {
		if !a.Yes || m.Draft == nil {
			return record.DraftRef{}, false
		}
		return *m.Draft, true
	}
	i := slices.IndexFunc(m.Options, func(o record.Option) bool { return o.ID == a.OptionID })
	if a.OptionID == "" || i < 0 || m.Options[i].Draft == nil {
		return record.DraftRef{}, false
	}
	return *m.Options[i].Draft, true
}

// ofTurn is draft id and its gate when the draft is one m's turn proposed on m's unit; false otherwise.
func (s *Service) ofTurn(ctx context.Context, m message.Message, id int64) (draft.Draft, gate.Gate, bool, error) {
	d, dg, ok, err := s.Drafts.DraftGate(ctx, id)
	if err != nil || !ok {
		return draft.Draft{}, gate.Gate{}, false, err
	}
	onUnit := d.Unit == m.Unit || d.Origin.Unit == m.Unit
	byTurn := (d.Origin.Kind == draft.OriginTurn || d.Origin.Kind == draft.OriginSession) && d.Origin.TurnSeq == m.Origin.TurnSeq
	ofDraft := dg.Kind == gate.KindDraft && dg.Subject == strconv.FormatInt(id, 10)
	return d, dg, int64(d.ID) == id && onUnit && byTurn && ofDraft, nil
}
