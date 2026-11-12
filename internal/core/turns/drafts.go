package turns

import (
	"context"
	"slices"
	"strconv"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// DraftGates reads a draft and the gate it waited on, as the outbox keeps them.
type DraftGates interface {
	// Draft is draft id; false when there is none.
	Draft(ctx context.Context, id draft.ID) (draft.Draft, bool, error)
	GateOf(ctx context.Context, d draft.Draft) (gate.Gate, bool, error)
}

// WithoutSourceWrites is p with writes, the command prefixes the plugins' manifests declare as writes to
// their source, denied when s, the session of an intent, sets source_writes = false.
func (p Policy) WithoutSourceWrites(s dintent.Session, writes [][]string) Policy {
	if !s.NoSourceWrites {
		return p
	}
	p.Deny = append(slices.Clone(p.Deny), writes...)
	return p
}

// DraftSent ends the turn d.Origin.TurnSeq names now that d is sent: an asked turn whose question
// points to d ends done, and a done turn whose outcome left d in its url's place gets d's url. The url is
// d's evidence permalink and the verdict the one DraftVerdict reads from d, never the agent's. The outcome
// says the user approved d only when d's gate was resolved by a person, or a session the user drives that
// neither proposed nor revised d, at the rev the turn pinned; a rule's or a window's release never does.
// A draft of no turn, or of a turn that points elsewhere, ends nothing, and a call made again changes
// nothing more.
func (s *Service) DraftSent(ctx context.Context, d draft.Draft) error {
	if d.State != draft.Sent {
		return nil
	}
	return s.settleDraft(ctx, d, s.sentEnd(ctx, d))
}

// DraftRecorded fills, when turn id on u has just recorded done with outcome.draft ref and no url, the url
// of that draft if it is sent already, as DraftSent does when the send comes after the record: a rule may
// release and post a draft before its turn records it. Only a draft of that turn, or of its session, on u
// counts.
func (s *Service) DraftRecorded(ctx context.Context, u unit.Unit, id string, ref record.DraftRef) error {
	if s.DraftGates == nil {
		return nil
	}
	d, ok, err := s.DraftGates.Draft(ctx, draft.ID(ref.ID))
	if err != nil || !ok || d.State != draft.Sent || !d.Concerns(u) {
		return err
	}
	s.mu.Lock()
	b, err := s.book(ctx, u)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	t, ok := b.ByID(id)
	if !ok || !d.Origin.ByAgent() ||
		d.Origin.TurnSeq != t.Seq && (t.Session == "" || d.Origin.Session != t.Session) {
		return nil
	}
	return s.settleTurn(ctx, u, t.Seq, s.sentEnd(ctx, d))
}

// sentEnd is the record a turn ends with once sent draft d is in it, as DraftSent describes.
func (s *Service) sentEnd(ctx context.Context, d draft.Draft) func(t turn.Turn, b *turn.Book, last record.Record) (record.Record, bool, error) {
	return func(t turn.Turn, b *turn.Book, last record.Record) (record.Record, bool, error) {
		rec, ok := sentRecord(t, last, int64(d.ID))
		if !ok {
			return record.Record{}, false, nil
		}
		o := rec.Outcome
		o.URL, o.Verdict = d.Evidence.Permalink, ""
		if s.DraftVerdict != nil {
			o.Verdict = s.DraftVerdict(d)
		}
		g, found, err := s.draftGate(ctx, d)
		if err != nil {
			return record.Record{}, false, err
		}
		pinned := o.Draft.Rev
		o.Draft.Rev = d.Rev
		o.Approved = found && UserApproved(g, d) && g.ApprovedRev == d.Rev && d.Rev <= pinned
		return rec, true, nil
	}
}

// DraftEnded ends, when d failed or was dropped, the asked turn that waits on d because the user's answer
// approved it: the turn ends done with d in its outcome and no url, approved by nobody, so the unit is
// free for the round its draft facts start. A turn that only points to d, with no such answer, still asks.
func (s *Service) DraftEnded(ctx context.Context, d draft.Draft) error {
	if d.State != draft.Failed && d.State != draft.Dropped {
		return nil
	}
	return s.settleDraft(ctx, d, func(t turn.Turn, b *turn.Book, last record.Record) (record.Record, bool, error) {
		if t.State != turn.Asked || last.State != record.Asked {
			return record.Record{}, false, nil
		}
		ref, ok := approvedRef(b, t.Seq, last.Question, int64(d.ID))
		if !ok {
			return record.Record{}, false, nil
		}
		out := record.Outcome{Draft: &ref}
		return record.Record{State: record.Done, Summary: last.Summary, Next: last.Next, Outcome: &out}, true, nil
	})
}

// settleDraft records, for the turn d.Origin.TurnSeq names, the record end returns, unless it returns
// none or repeats the turn's last record.
func (s *Service) settleDraft(ctx context.Context, d draft.Draft,
	end func(t turn.Turn, b *turn.Book, last record.Record) (record.Record, bool, error)) error {
	if d.Origin.TurnSeq == 0 {
		return nil
	}
	u := d.Origin.Unit
	if u.IsZero() {
		u = d.Unit
	}
	return s.settleTurn(ctx, u, d.Origin.TurnSeq, end)
}

// settleTurn records, for turn seq on u, the record end returns, unless it returns none or repeats the
// turn's last record.
func (s *Service) settleTurn(ctx context.Context, u unit.Unit, seq int64,
	end func(t turn.Turn, b *turn.Book, last record.Record) (record.Record, bool, error)) error {
	s.mu.Lock()
	b, err := s.book(ctx, u)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	t, ok := bySeq(&b, seq)
	if !ok {
		return nil
	}
	last, ok := b.LastRecord(t.Seq)
	if !ok {
		return nil
	}
	rec, ok, err := end(*t, &b, last.Record)
	if err != nil || !ok || rec.Hash() == last.Record.Hash() {
		return err
	}
	_, err = s.closeTurn(ctx, u, *t, last.ID, rec)
	return err
}

// sentRecord is the done record turn t, whose last record is last, ends with once draft id is sent;
// false when t does not wait on that draft.
func sentRecord(t turn.Turn, last record.Record, id int64) (record.Record, bool) {
	out := record.Outcome{}
	if last.Outcome != nil {
		out = *last.Outcome
	}
	switch {
	case t.State == turn.Asked && last.State == record.Asked:
		ref, ok := pointedDraft(last.Question, id)
		if !ok {
			return record.Record{}, false
		}
		out.Draft = &ref
	case t.State == turn.Done && last.State == record.Done && out.Draft != nil && out.Draft.ID == id && !out.Approved && out.URL == "":
		ref := *out.Draft
		out.Draft = &ref
	default:
		return record.Record{}, false
	}
	return record.Record{State: record.Done, Summary: last.Summary, Next: last.Next, Outcome: &out}, true
}

// approvedRef is the ref to draft id an answer of turn seq routed approved picked on q, since the turn's
// last record; false when no such answer picked it.
func approvedRef(b *turn.Book, seq int64, q *record.Question, id int64) (record.DraftRef, bool) {
	if q == nil {
		return record.DraftRef{}, false
	}
	last, _ := b.LastRecord(seq)
	for _, a := range b.Answers {
		if a.TurnSeq != seq || a.Route != turn.Approved || a.At.Before(last.At) {
			continue
		}
		if q.Kind == "confirm" && a.Yes && q.Draft != nil && q.Draft.ID == id {
			return *q.Draft, true
		}
		for _, o := range q.Options {
			if o.ID == a.OptionID && o.Draft != nil && o.Draft.ID == id {
				return *o.Draft, true
			}
		}
	}
	return record.DraftRef{}, false
}

// pointedDraft is the draft ref q's options, or its confirm, point to as draft id.
func pointedDraft(q *record.Question, id int64) (record.DraftRef, bool) {
	if q == nil {
		return record.DraftRef{}, false
	}
	if q.Draft != nil && q.Draft.ID == id {
		return *q.Draft, true
	}
	for _, o := range q.Options {
		if o.Draft != nil && o.Draft.ID == id {
			return *o.Draft, true
		}
	}
	return record.DraftRef{}, false
}

func (s *Service) draftGate(ctx context.Context, d draft.Draft) (gate.Gate, bool, error) {
	if s.DraftGates == nil {
		return gate.Gate{}, false, nil
	}
	return s.DraftGates.GateOf(ctx, d)
}

// UserApproved says whether g, d's gate, approved d by the user: a person, or a session the user drives
// that is not one that proposed or revised d.
func UserApproved(g gate.Gate, d draft.Draft) bool {
	if g.Kind != gate.KindDraft || g.Subject != strconv.FormatInt(int64(d.ID), 10) || g.Status != gate.Approved || !g.ByUser() {
		return false
	}
	return !g.Stamps(g.BySession)
}
