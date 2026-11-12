package turns

import (
	"context"
	"slices"
	"strconv"

	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// DraftGates reads the gate a draft waited on, as outbox.Service does.
type DraftGates interface {
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
// d's evidence permalink. The outcome says the user approved d only when d's gate was resolved by a
// person, or a session the user drives that neither proposed nor revised d; a rule's or a window's
// release never does. A draft of no turn, or of a turn that points elsewhere, ends nothing, and a call
// made again changes nothing more.
func (s *Service) DraftSent(ctx context.Context, d draft.Draft) error {
	if d.State != draft.Sent || d.Origin.TurnSeq == 0 {
		return nil
	}
	u := d.Origin.Unit
	if u.IsZero() {
		u = d.Unit
	}
	s.mu.Lock()
	b, err := s.book(ctx, u)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	t, ok := bySeq(&b, d.Origin.TurnSeq)
	if !ok {
		return nil
	}
	last, ok := b.LastRecord(t.Seq)
	if !ok {
		return nil
	}
	rec, ok := sentRecord(*t, last.Record, int64(d.ID))
	if !ok {
		return nil
	}
	rec.Outcome.URL = d.Evidence.Permalink
	if rec.Outcome.Approved, err = s.approvedByUser(ctx, d); err != nil {
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
	case t.State == turn.Done && last.State == record.Done && out.Draft != nil && out.Draft.ID == id && out.URL == "":
		ref := *out.Draft
		out.Draft = &ref
	default:
		return record.Record{}, false
	}
	return record.Record{State: record.Done, Summary: last.Summary, Next: last.Next, Outcome: &out}, true
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

func (s *Service) approvedByUser(ctx context.Context, d draft.Draft) (bool, error) {
	if s.DraftGates == nil {
		return false, nil
	}
	g, ok, err := s.DraftGates.GateOf(ctx, d)
	if err != nil || !ok {
		return false, err
	}
	return UserApproved(g, d), nil
}

// UserApproved says whether g, d's gate, approved d by the user: a person, or a session the user drives
// that is not one that proposed or revised d.
func UserApproved(g gate.Gate, d draft.Draft) bool {
	if g.Kind != gate.KindDraft || g.Subject != strconv.FormatInt(int64(d.ID), 10) || g.Status != gate.Approved || !g.ByUser() {
		return false
	}
	return !draftSession(g, d, g.BySession)
}

// draftSession says whether session, when not "", proposed or revised d, whose gate is g.
func draftSession(g gate.Gate, d draft.Draft, session string) bool {
	return session != "" && (session == g.Session || session == d.Origin.Session || slices.Contains(g.Sessions, session))
}
