package facttask

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// RefusalDeps are what task.refused_pairs and task.undone read, with Deps.Gates.
type RefusalDeps struct {
	// Pairs says whether verb v merges or relates two tasks, as the profile's handler of it says; nil: none does.
	Pairs func(v task.Verb) bool
	// Splits are the verbs that give a merged task back, as the profile's handlers say; nil: none.
	Splits func() []string
	// Unrelates are the verbs that take a relation off, as the profile's handlers say; nil: none.
	Unrelates func() []string
	// Events are every unit's events of kinds, oldest first; nil: no split or unrelation is read.
	Events func(ctx context.Context, kinds []string) ([]workflow.Event, error)
}

const passRefused = "facts.refused_pairs"

// refusedPairs is task.refused_pairs of e: the tasks the user took apart from e, as undone lists them, while no
// item either task holds was created or changed after they did, sorted. Updated is not read: seeing a task moves it.
func (p *Provider) refusedPairs(ctx context.Context, e task.Task) ([]string, error) {
	return p.apart(ctx, e, true)
}

// undone is task.undone of e: the tasks a rejected merge or relate gate paired e with, that a split took apart
// from it or that an unrelation took off it, whatever changed since, sorted.
func (p *Provider) undone(ctx context.Context, e task.Task) ([]string, error) {
	return p.apart(ctx, e, false)
}

// TakenApart says whether other is one of task.undone of e.
func (p *Provider) TakenApart(ctx context.Context, e task.Task, other task.ID) (bool, error) {
	undone, err := p.undone(ctx, e)
	return slices.Contains(undone, string(other)), err
}

// apart are the tasks the refusals pair e with, as they read now, those whose items moved since dropped when
// expire is set.
func (p *Provider) apart(ctx context.Context, e task.Task, expire bool) ([]string, error) {
	out := []string{}
	if p.d.Gates == nil || p.d.Refusals.Pairs == nil {
		return out, nil
	}
	rs, err := coretasks.Once(ctx, passRefused, func() ([]merge.Undone, error) { return p.refusals(ctx) })
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		a, b, err := p.resolvePair(ctx, r)
		if err != nil {
			return nil, err
		}
		var o task.Task
		switch {
		case a.ID == "" || b.ID == "" || a.ID == b.ID:
			continue
		case a.ID == e.ID:
			o = b
		case b.ID == e.ID:
			o = a
		default:
			continue
		}
		if slices.Contains(out, string(o.ID)) {
			continue
		}
		if expire {
			moved, err := p.itemsMovedAfter(ctx, r.At, e, o)
			if err != nil {
				return nil, err
			}
			if moved {
				continue
			}
		}
		out = append(out, string(o.ID))
	}
	slices.Sort(out)
	return out, nil
}

// resolvePair is r's two tasks under the ids they read as now, a side merged away into its survivor; a side
// that is gone reads as the zero task.
func (p *Provider) resolvePair(ctx context.Context, r merge.Undone) (task.Task, task.Task, error) {
	var out [2]task.Task
	for i, id := range []task.ID{r.A, r.B} {
		o, err := p.passTask(ctx, id)
		switch {
		case errors.Is(err, coretasks.ErrNoTask):
		case err != nil:
			return task.Task{}, task.Task{}, err
		default:
			out[i] = o
		}
	}
	return out[0], out[1], nil
}

// refusals are the pairs the rejected proposal gates that proposed a merge or a relation, the splits and the
// unrelations took apart.
func (p *Provider) refusals(ctx context.Context) ([]merge.Undone, error) {
	gs, err := p.d.Gates.Where(ctx, gates.Query{Kind: gate.KindProposal, Statuses: []gate.Status{gate.Rejected}})
	if err != nil {
		return nil, err
	}
	var rejected []merge.Undone
	for _, g := range gs {
		req, ok := coretasks.ProposedVerb(g)
		other := task.ID(req.Args[task.ArgTask])
		if !ok || g.Task == "" || other == "" || !p.d.Refusals.Pairs(req.Verb) {
			continue
		}
		rejected = append(rejected, merge.Undone{A: g.Task, B: other, At: g.ClosedAt})
	}
	if p.d.Refusals.Events == nil {
		return rejected, nil
	}
	var splits, unrelates []string
	if p.d.Refusals.Splits != nil {
		splits = p.d.Refusals.Splits()
	}
	if p.d.Refusals.Unrelates != nil {
		unrelates = p.d.Refusals.Unrelates()
	}
	kinds := merge.UndoneKinds(splits, unrelates)
	if len(kinds) == 0 {
		return rejected, nil
	}
	evs, err := p.d.Refusals.Events(ctx, kinds)
	if err != nil {
		return nil, err
	}
	return merge.UndonePairs(rejected, evs, splits, unrelates), nil
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
