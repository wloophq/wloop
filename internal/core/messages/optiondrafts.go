package messages

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// ReasonOtherOption is why a draft another option pointed to is withdrawn once one option's draft is approved.
const ReasonOtherOption = "other_option"

// OptionDrafts are the drafts a question's options point to, as the outbox and the gates service keep them.
type OptionDrafts interface {
	// DraftGate is draft id and the newest gate it waits or waited on; false when there is no such draft or gate.
	DraftGate(ctx context.Context, id int64) (draft.Draft, gate.Gate, bool, error)
	// WithdrawOther withdraws draft id, another option's, with reason, whatever it waits on: held, queued or
	// failed; one sending or sent is left as it is.
	WithdrawOther(ctx context.Context, id int64, reason string) error
}

// OptionDraft reports the pin r's answer to question gate question picks: the gate of the draft the option,
// or a confirm's yes, points to in the message the person saw, held by that message's turn, or a turn of its
// chain, on its unit, and
// the rev the option pinned. The gates service owns the rest of the option's approval (ApproveOption):
// whether the draft's gate still waits at that rev and whether r's resolver may approve it. False for an
// answer that picks no held draft, which then resolves the question alone and resumes the turn.
func (s *Service) OptionDraft(ctx context.Context, question gate.Gate, r gates.Resolution) (gate.Gate, int64, bool, error) {
	if s.Drafts == nil || question.Kind != gate.KindQuestion {
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
	d, dg, ok, err := s.turnDrafts(m).of(ctx, ref.ID)
	switch {
	case err != nil:
		return gate.Gate{}, 0, false, err
	case !ok || d.State != draft.Held:
		s.logf("messages: message %d points to draft %d, which its turn does not hold on its unit; the turn resumes", m.ID, ref.ID)
		return gate.Gate{}, 0, false, nil
	}
	return dg, ref.Rev, true, nil
}

// approvedOption says whether g, m's answered gate, approved the draft the answer a picks, as
// OptionDraft named it: the draft gate approved by g's own resolver. The gates service let that resolver
// approve it (a person, or a session the user drives the gate does not stamp) at the pinned rev, which stays
// the draft's until its content changes. It then
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
	held := s.turnDrafts(m)
	_, dg, ok, err := held.of(ctx, ref.ID)
	switch {
	case err != nil:
		return false, err
	case !ok:
		return false, nil
	case dg.Status != gate.Approved || dg.By != g.By || dg.BySession != g.BySession || dg.Rule != "":
		return false, nil
	}
	for _, o := range m.Options {
		if o.Draft == nil || o.Draft.ID == ref.ID {
			continue
		}
		if _, _, ok, err := held.of(ctx, o.Draft.ID); err != nil || !ok {
			continue
		}
		if err := s.Drafts.WithdrawOther(ctx, o.Draft.ID, ReasonOtherOption); err != nil {
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

// turnDrafts are the drafts m's turn holds, reading the turn's book once.
type turnDrafts struct {
	s    *Service
	m    message.Message
	book *turn.Book
}

func (s *Service) turnDrafts(m message.Message) *turnDrafts { return &turnDrafts{s: s, m: m} }

// of is draft id and its gate when the draft is one m's turn, or a turn of its chain, proposed on m's unit;
// false otherwise.
func (t *turnDrafts) of(ctx context.Context, id int64) (draft.Draft, gate.Gate, bool, error) {
	d, dg, ok, err := t.s.Drafts.DraftGate(ctx, id)
	if err != nil || !ok {
		return draft.Draft{}, gate.Gate{}, false, err
	}
	ofDraft := dg.Kind == gate.KindDraft && dg.Subject == strconv.FormatInt(id, 10)
	if int64(d.ID) != id || !d.Concerns(t.m.Unit) || !ofDraft {
		return d, dg, false, nil
	}
	if t.book == nil {
		books, err := t.s.Books.Books(ctx, t.m.Unit)
		if err != nil {
			return draft.Draft{}, gate.Gate{}, false, err
		}
		b := books[t.m.Unit]
		b.Unit = t.m.Unit
		t.book = &b
	}
	return d, dg, t.book.Proposed(t.m.Origin.TurnSeq, d), nil
}

// Pinning is the question gate whose open message pins the draft of draft gate drafted, in an option or its
// confirm, the answer that picks it and the rev it pinned; false when no open message of the draft's turn, or of a
// turn resuming it, pins it.
// A question answered while its message still settles (its other options' drafts not yet withdrawn) still names
// the pin, so an approval in that window meets the answered question, not a free draft; the gate is zero while
// the message's question gate is not opened yet.
func (s *Service) Pinning(ctx context.Context, drafted gate.Gate) (gate.Gate, string, int64, bool, error) {
	if s.Drafts == nil || drafted.Kind != gate.KindDraft {
		return gate.Gate{}, "", 0, false, nil
	}
	id, err := strconv.ParseInt(drafted.Subject, 10, 64)
	if err != nil {
		return gate.Gate{}, "", 0, false, nil
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return gate.Gate{}, "", 0, false, err
	}
	for _, m := range open {
		answer, ref, ok := pinOf(m, id)
		if !ok {
			continue
		}
		if _, _, held, err := s.turnDrafts(m).of(ctx, id); err != nil || !held {
			if err != nil {
				return gate.Gate{}, "", 0, false, err
			}
			continue
		}
		gs, err := s.Gates.Where(ctx, gates.Query{Kind: gate.KindQuestion, Unit: m.Unit, Statuses: []gate.Status{gate.Open, gate.Answered}})
		if err != nil {
			return gate.Gate{}, "", 0, false, err
		}
		for _, g := range gs {
			if g.Subject == strconv.FormatInt(m.ID, 10) {
				return g, GateAnswer(answer), ref.Rev, true, nil
			}
		}
		return gate.Gate{}, GateAnswer(answer), ref.Rev, true, nil
	}
	return gate.Gate{}, "", 0, false, nil
}

// pinOf is the answer to question m that picks draft id, and the pin it holds; false when m pins no such draft.
func pinOf(m message.Message, id int64) (Answer, record.DraftRef, bool) {
	if m.Kind != message.Question {
		return Answer{}, record.DraftRef{}, false
	}
	if m.Confirm && m.Draft != nil && m.Draft.ID == id {
		return Answer{Yes: true}, *m.Draft, true
	}
	for _, o := range m.Options {
		if o.Draft != nil && o.Draft.ID == id {
			return Answer{OptionID: o.ID}, *o.Draft, true
		}
	}
	return Answer{}, record.DraftRef{}, false
}

// PinnedDrafts are the drafts the open questions pin, in an option or a confirm, each with the id of the message
// that pins it: a draft its question's turn, or a turn of its chain, holds on the question's unit, which only an
// answer to that question decides.
func (s *Service) PinnedDrafts(ctx context.Context) (map[int64]int64, error) {
	if s.Drafts == nil {
		return nil, nil
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]int64{}
	for _, m := range open {
		if m.Kind != message.Question {
			continue
		}
		held := s.turnDrafts(m)
		refs := []*record.DraftRef{m.Draft}
		for _, o := range m.Options {
			refs = append(refs, o.Draft)
		}
		for _, ref := range refs {
			if ref == nil {
				continue
			}
			if _, _, ok, err := held.of(ctx, ref.ID); err != nil {
				return nil, err
			} else if ok {
				out[ref.ID] = m.ID
			}
		}
	}
	return out, nil
}
