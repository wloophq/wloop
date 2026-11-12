package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Gates holds the decision each draft waits on: a draft gate whose Subject
// is the draft's ID. Its resolution reaches the outbox through Publish.
type Gates interface {
	Open(ctx context.Context, g gate.Gate, p gates.Policy) (gate.Gate, error)
	Supersede(ctx context.Context, id int64, by task.Actor) error
	// SupersedeForAgent closes open draft gate id because session revised its draft.
	SupersedeForAgent(ctx context.Context, id int64, session string) (gate.Gate, error)
	RecordSent(ctx context.Context, g gate.Gate) (gate.Gate, error)
	Where(ctx context.Context, q gates.Query) ([]gate.Gate, error)
}

// The Reasons of a dropped draft the outbox writes.
const (
	reasonUser    = "user"
	reasonDryRun  = string(draft.DryRun)
	reasonUngated = "ungated"
	reasonGone    = "superseded"
)

// everyStatus are the statuses a draft's gate may have.
var everyStatus = []gate.Status{gate.Open, gate.Approved, gate.Rejected, gate.Answered, gate.Dismissed, gate.Superseded}

// gateOf is the gate d waits on at its stored rev, before it opens, stamped with the sessions that proposed
// or revised it.
func gateOf(d draft.Draft, sessions []string) gate.Gate {
	if d.Origin.Session != "" && !slices.Contains(sessions, d.Origin.Session) {
		sessions = append([]string{d.Origin.Session}, sessions...)
	}
	return gate.Gate{Kind: gate.KindDraft, Unit: d.Unit, Task: task.ID(d.Task), Subject: strconv.FormatInt(int64(d.ID), 10),
		Session: d.Origin.Session, ApprovedRev: d.Rev, Sessions: sessions}
}

// sessionsOf are the sessions the gate a revision opens stamps: those prev, the draft's gate before it,
// stamped, and session, the agent's that revises it.
func sessionsOf(prev gate.Gate, ok bool, session string) []string {
	var out []string
	if ok {
		out = append(out, prev.Sessions...)
		if prev.Session != "" {
			out = append(out, prev.Session)
		}
	}
	if session != "" {
		out = append(out, session)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// TaskPlacer is optional on Gates: the task a gate g would be placed on when it opens, so a draft takes it
// before its gate opens at its rev.
type TaskPlacer interface {
	TaskOf(ctx context.Context, g gate.Gate) (task.ID, error)
}

// taskOf is the task the gate of d, which names none, opens on; "" when Gates cannot say before it opens,
// and the draft then adopts its gate's.
func (s *Service) taskOf(ctx context.Context, d draft.Draft) (string, error) {
	tp, ok := s.Gates.(TaskPlacer)
	if !ok {
		return "", nil
	}
	id, err := tp.TaskOf(ctx, gateOf(d, nil))
	return string(id), err
}

// atGateRev says whether held draft d is at the content rev its gate g opened for; a draft starts at rev 1,
// so a gate of rev 0 never matches. A write that changes no content, the adoption of its task, keeps it.
func atGateRev(d draft.Draft, g gate.Gate) bool {
	return g.ApprovedRev >= 1 && d.Rev == g.ApprovedRev
}

// decided is the gates' Policy of the decision a draft's approval made.
type decided draft.Decision

func (p decided) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	out := gates.Decision{Mode: gate.Mode(p.Approval.Mode), Window: p.Approval.Window}
	if p.Rule >= 0 {
		out.Rule = fmt.Sprintf("gates.approval rule %d", p.Rule)
	}
	return out, nil
}

func subjectID(g gate.Gate) (draft.ID, bool) {
	n, err := strconv.ParseInt(g.Subject, 10, 64)
	return draft.ID(n), err == nil && n > 0
}

// Publish is the consequence of draft gate g's resolution on its held
// draft: approved queues it for the sender, any other status drops it. A
// draft a rule or a window released fails instead, for a person to retry,
// when its publisher now posts it as another delivery than the one its
// approval decided, or when the workflow that proposed it no longer loads.
func (s *Service) Publish(ctx context.Context, g gate.Gate) error {
	id, ok := subjectID(g)
	if g.Kind != gate.KindDraft || !ok {
		return nil
	}
	d, err := s.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != draft.Held {
		return nil
	}
	switch g.Status {
	case gate.Approved:
	case gate.Rejected:
		reason := reasonUser
		if !g.ByUser() {
			reason = reasonDryRun
		}
		_, err = s.drop(ctx, d, reason)
		return err
	default:
		_, err = s.drop(ctx, d, reasonGone)
		return err
	}
	if !atGateRev(d, g) {
		s.logf("draft %d failed on its release: its gate approved rev %d, it is at %d", d.ID, g.ApprovedRev, d.Rev)
		return s.failHeld(ctx, d, fmt.Sprintf("it changed after its gate opened: its gate approved rev %d, it is at rev %d", g.ApprovedRev, d.Rev))
	}
	if !g.ByUser() {
		if why := s.unreleasable(d); why != "" {
			s.logf("draft %d failed on its release: %s", d.ID, why)
			return s.failHeld(ctx, d, why)
		}
	}
	if err := s.fresh(ctx, d); err != nil {
		return s.failHeld(ctx, d, failure(err))
	}
	if err := s.publishable(ctx, &d); err != nil {
		return s.failHeld(ctx, d, failure(err))
	}
	d.State = draft.Queued
	if _, err = s.save(ctx, d, draft.Held); !errors.Is(err, ErrConflict) {
		return err
	}
	if d, err = s.Store.Get(ctx, id); err != nil || d.State != draft.Held {
		return err
	}
	return s.failHeld(ctx, d, "it changed as its gate released it")
}

// unreleasable is why no rule may release d now, "" when one may.
func (s *Service) unreleasable(d draft.Draft) string {
	if d.Hold {
		return "it is held for a person, and a rule or a window released it"
	}
	if got := s.delivered(d).Delivery; got != d.Delivery {
		return fmt.Sprintf("its publisher now posts it as a %s, not the %s its approval decided", got, d.Delivery)
	}
	if s.Broken != nil && d.Origin.Workflow != "" && slices.Contains(s.Broken(), d.Origin.Workflow) {
		return fmt.Sprintf("its workflow %s does not load", d.Origin.Workflow)
	}
	return ""
}

func (s *Service) failHeld(ctx context.Context, d draft.Draft, reason string) error {
	d.State, d.Error = draft.Failed, reason
	_, err := s.save(ctx, d, draft.Held)
	return err
}

// GateOf is d's gate; false when it has none.
func (s *Service) GateOf(ctx context.Context, d draft.Draft) (gate.Gate, bool, error) {
	if s.Gates == nil {
		return gate.Gate{}, false, nil
	}
	gs, err := s.Gates.Where(ctx, gates.Query{Kind: gate.KindDraft, Unit: d.Unit, Statuses: everyStatus})
	if err != nil {
		return gate.Gate{}, false, err
	}
	subject := strconv.FormatInt(int64(d.ID), 10)
	for _, g := range slices.Backward(gs) {
		if g.Subject == subject {
			return g, true, nil
		}
	}
	return gate.Gate{}, false, nil
}

// Pending is a draft the Outbox shows, with its gate.
type Pending struct {
	Draft draft.Draft
	Gate  gate.Gate
}

// Outbox are the drafts waiting on the user, newest first: those whose
// gate is open, and those whose gate approved them and whose delivery failed
// or a restart has yet to settle.
func (s *Service) Outbox(ctx context.Context) ([]Pending, error) {
	if s.Gates == nil {
		return nil, ErrNoGates
	}
	gs, err := s.Gates.Where(ctx, gates.Query{Kind: gate.KindDraft, Statuses: []gate.Status{gate.Open, gate.Approved}})
	if err != nil {
		return nil, err
	}
	ds, err := s.List(ctx, Filter{States: []draft.State{draft.Held, draft.Sending, draft.Failed}})
	if err != nil {
		return nil, err
	}
	byID := make(map[draft.ID]draft.Draft, len(ds))
	for _, d := range ds {
		byID[d.ID] = d
	}
	var out []Pending
	for _, g := range gs {
		id, ok := subjectID(g)
		d, listed := byID[id]
		if ok && listed && (g.Status == gate.Open) == (d.State == draft.Held) {
			out = append(out, Pending{Draft: d, Gate: g})
		}
	}
	slices.SortStableFunc(out, func(a, b Pending) int { return int(b.Draft.ID - a.Draft.ID) })
	return out, nil
}

// DeliveryOf is where the delivery of draft gate g's draft stands; "" for
// a gate of another kind.
func (s *Service) DeliveryOf(ctx context.Context, g gate.Gate) (draft.State, error) {
	id, ok := subjectID(g)
	if g.Kind != gate.KindDraft || !ok {
		return "", nil
	}
	d, err := s.Store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return d.State, err
}

// Rehomed asks a person again for each draft of units that a move to another unit took past the rev its gate
// approved or waits on: a held one waits on a new gate at its rev, and a queued one goes back to held under
// one, so its old approval neither posts it nor fails it.
func (s *Service) Rehomed(ctx context.Context, units ...unit.Unit) error {
	var errs []error
	for _, u := range units {
		ds, err := s.Store.List(ctx, Filter{States: []draft.State{draft.Held, draft.Queued}, Unit: &u})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, d := range ds {
			g, ok, err := s.GateOf(ctx, d)
			switch {
			case err != nil:
				errs = append(errs, err)
				continue
			case ok && g.ApprovedRev == d.Rev:
				continue
			}
			if d.State == draft.Queued {
				d.State = draft.Held
				if d, err = s.save(ctx, d, draft.Queued); err != nil {
					errs = append(errs, ignoreConflict(err))
					continue
				}
			}
			errs = append(errs, s.waitAt(ctx, d, sessionsOf(g, ok, ""), ""))
		}
	}
	return errors.Join(errs...)
}
