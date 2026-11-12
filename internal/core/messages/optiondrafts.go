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
)

// ReasonOtherOption is why a draft another option pointed to is withdrawn once one option's draft is approved.
const ReasonOtherOption = "other_option"

// OptionDrafts are the drafts a question's options point to, as the outbox and the gates service keep them.
type OptionDrafts interface {
	// DraftGate is draft id and the newest gate it waits or waited on; false when there is no such draft or gate.
	DraftGate(ctx context.Context, id int64) (draft.Draft, gate.Gate, bool, error)
	// ApproveOption approves drafted at rev with by, who resolved question, as gates.Service.ApproveOption does.
	ApproveOption(ctx context.Context, question, drafted gate.Gate, rev int64, by gate.Resolver) error
	// WithdrawHeld withdraws draft id while it is still held, with reason; one no longer held is left as it is.
	WithdrawHeld(ctx context.Context, id int64, reason string) error
}

// approveOption approves the draft the option a picks on question message m points to, when g, m's gate,
// was resolved by the user, and withdraws the drafts the other options point to; it says whether it did,
// so the answer ends the turn on the draft's send instead of resuming it. The draft must be held by m's
// turn on m's unit at the option's rev, and the resolver must be neither a session that proposed or
// revised it nor the turn's; else the pick resumes the turn as any answer does. A call made again after
// the approval completes what is left.
func (s *Service) approveOption(ctx context.Context, g gate.Gate, m message.Message, a Answer) (bool, error) {
	i := slices.IndexFunc(m.Options, func(o record.Option) bool { return o.ID == a.OptionID })
	if s.Drafts == nil || m.Kind != message.Question || a.OptionID == "" || i < 0 || m.Options[i].Draft == nil {
		return false, nil
	}
	ref := *m.Options[i].Draft
	if g.Kind != gate.KindQuestion || g.Status != gate.Answered || !g.ByUser() || g.BySession != "" && g.BySession == g.Session {
		return false, nil
	}
	d, dg, ok, err := s.ofTurn(ctx, m, ref.ID)
	switch {
	case err != nil:
		return false, err
	case !ok || turns.ProposedOrRevised(dg, d, g.BySession):
		s.logf("messages: option %s of message %d points to draft %d, which its pick does not approve; the turn resumes", a.OptionID, m.ID, ref.ID)
		return false, nil
	}
	by := gate.Resolver{By: g.By, Session: g.BySession}
	switch {
	case dg.Status == gate.Open && d.State == draft.Held:
		err := s.Drafts.ApproveOption(ctx, g, dg, ref.Rev, by)
		if errors.Is(err, gates.ErrRefused) {
			s.logf("messages: the gates refused draft %d at rev %d for option %s of message %d: %v; the turn resumes", ref.ID, ref.Rev, a.OptionID, m.ID, err)
			return false, nil
		}
		if err != nil {
			return false, err
		}
	case dg.Status == gate.Approved && dg.ApprovedRev == ref.Rev && dg.By == by.By && dg.BySession == by.Session:
	default:
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
