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
)

// Gates holds the decision each draft waits on: a draft gate whose Subject
// is the draft's ID. Its resolution reaches the outbox through Publish.
type Gates interface {
	Open(ctx context.Context, g gate.Gate, p gates.Policy) (gate.Gate, error)
	Supersede(ctx context.Context, id int64, by task.Actor) error
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

// gateOf is the gate d waits on, before it opens.
func gateOf(d draft.Draft) gate.Gate {
	return gate.Gate{Kind: gate.KindDraft, Unit: d.Unit, Task: task.ID(d.Task), Subject: strconv.FormatInt(int64(d.ID), 10)}
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
		if g.By != task.ActorPerson {
			reason = reasonDryRun
		}
		_, err = s.drop(ctx, d, reason)
		return err
	default:
		_, err = s.drop(ctx, d, reasonGone)
		return err
	}
	if g.By != task.ActorPerson {
		if why := s.unreleasable(d); why != "" {
			s.logf("draft %d failed on its release: %s", d.ID, why)
			return s.failHeld(ctx, d, why)
		}
	}
	if err := s.publishable(ctx, &d); err != nil {
		return s.failHeld(ctx, d, err.Error())
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
