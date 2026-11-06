package facttask

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// RefusalDeps are what task.refused_merges reads, with Deps.Gates.
type RefusalDeps struct {
	// Merges says whether verb v merges two tasks, as the profile's handler of it says; nil: none does.
	Merges func(v task.Verb) bool
}

const passRefused = "facts.refused_merges"

// refusal is a rejected gate that proposed merging a and b, closed at.
type refusal struct {
	a, b   task.ID
	closed time.Time
}

// refusedMerges is task.refused_merges of e: the tasks a rejected merge gate paired e with, while no item
// either task holds was created or changed after the gate closed, sorted. Updated is not read: seeing a task
// moves it.
func (p *Provider) refusedMerges(ctx context.Context, e task.Task) ([]string, error) {
	out := []string{}
	if p.d.Gates == nil || p.d.Refusals.Merges == nil {
		return out, nil
	}
	rs, err := coretasks.Once(ctx, passRefused, func() ([]refusal, error) { return p.refusals(ctx) })
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		other := r.b
		switch e.ID {
		case r.a:
		case r.b:
			other = r.a
		default:
			continue
		}
		o, err := p.passTask(ctx, other)
		switch {
		case errors.Is(err, coretasks.ErrNoTask):
			continue
		case err != nil:
			return nil, err
		}
		if o.ID != other || slices.Contains(out, string(o.ID)) {
			continue
		}
		moved, err := p.itemsMovedAfter(ctx, r.closed, e, o)
		if err != nil {
			return nil, err
		}
		if !moved {
			out = append(out, string(o.ID))
		}
	}
	slices.Sort(out)
	return out, nil
}

// refusals are the rejected proposal gates that proposed a merge.
func (p *Provider) refusals(ctx context.Context) ([]refusal, error) {
	gs, err := p.d.Gates.Where(ctx, gates.Query{Kind: gate.KindProposal, Statuses: []gate.Status{gate.Rejected}})
	if err != nil {
		return nil, err
	}
	var out []refusal
	for _, g := range gs {
		req, ok := coretasks.ProposedVerb(g)
		other := task.ID(req.Args[task.ArgTask])
		if !ok || g.Task == "" || other == "" || !p.d.Refusals.Merges(req.Verb) {
			continue
		}
		out = append(out, refusal{a: g.Task, b: other, closed: g.ClosedAt})
	}
	return out, nil
}

// itemsMovedAfter says whether one of the heldItems of es was created or changed after at.
func (p *Provider) itemsMovedAfter(ctx context.Context, at time.Time, es ...task.Task) (bool, error) {
	for _, e := range es {
		got, err := p.items(ctx, unitsOf(e))
		if err != nil {
			return false, err
		}
		for _, it := range heldItems(e, got) {
			if it.CreatedAt.After(at) || it.UpdatedAt.After(at) {
				return true, nil
			}
		}
	}
	return false, nil
}
