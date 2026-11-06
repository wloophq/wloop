package facttask

import (
	"context"
	"encoding/json"
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

// refusal is a rejected gate that proposed merging a and b, closed at.
type refusal struct {
	a, b   task.ID
	closed time.Time
}

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

// apart are the tasks the refusals pair e with, as they read now, those whose items moved since dropped when
// expire is set.
func (p *Provider) apart(ctx context.Context, e task.Task, expire bool) ([]string, error) {
	out := []string{}
	if p.d.Gates == nil || p.d.Refusals.Pairs == nil {
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
		if expire {
			moved, err := p.itemsMovedAfter(ctx, r.closed, e, o)
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

// refusals are the rejected proposal gates that proposed a merge or a relation, the splits and the unrelations.
func (p *Provider) refusals(ctx context.Context) ([]refusal, error) {
	gs, err := p.d.Gates.Where(ctx, gates.Query{Kind: gate.KindProposal, Statuses: []gate.Status{gate.Rejected}})
	if err != nil {
		return nil, err
	}
	var out []refusal
	for _, g := range gs {
		req, ok := coretasks.ProposedVerb(g)
		other := task.ID(req.Args[task.ArgTask])
		if !ok || g.Task == "" || other == "" || !p.d.Refusals.Pairs(req.Verb) {
			continue
		}
		out = append(out, refusal{a: g.Task, b: other, closed: g.ClosedAt})
	}
	splits, err := p.splits(ctx)
	if err != nil {
		return nil, err
	}
	unrelated, err := p.unrelations(ctx)
	return append(append(out, splits...), unrelated...), err
}

// unrelations are the relations an unrelate verb took off, at when it did: its event on a task's own unit keys
// the other task, as task.PairKey writes it.
func (p *Provider) unrelations(ctx context.Context) ([]refusal, error) {
	if p.d.Refusals.Unrelates == nil || p.d.Refusals.Events == nil {
		return nil, nil
	}
	verbs := p.d.Refusals.Unrelates()
	if len(verbs) == 0 {
		return nil, nil
	}
	evs, err := p.d.Refusals.Events(ctx, task.VerbEventKinds(verbs))
	if err != nil {
		return nil, err
	}
	var out []refusal
	for _, ev := range evs {
		id, ok := task.IDOf(ev.Unit)
		other, paired := task.PairOfKey(ev.Key)
		if ok && paired {
			out = append(out, refusal{a: id, b: other, closed: ev.At})
		}
	}
	return out, nil
}

// splits are the merges a split gave back, closed when it did: each merge record on a task's own unit and the
// first split event on that unit after it, which a split writes on both tasks' units.
func (p *Provider) splits(ctx context.Context) ([]refusal, error) {
	if p.d.Refusals.Splits == nil || p.d.Refusals.Events == nil {
		return nil, nil
	}
	verbs := p.d.Refusals.Splits()
	if len(verbs) == 0 {
		return nil, nil
	}
	evs, err := p.d.Refusals.Events(ctx, append(task.VerbEventKinds(verbs), merge.RecordKind))
	if err != nil {
		return nil, err
	}
	merged := map[task.ID]task.ID{}
	var out []refusal
	for _, ev := range evs {
		id, ok := task.IDOf(ev.Unit)
		if !ok {
			continue
		}
		if ev.Kind == merge.RecordKind {
			var r struct{ From, Into task.ID }
			if json.Unmarshal([]byte(ev.Data), &r) == nil && r.From == id && r.Into != "" {
				merged[id] = r.Into
			}
			continue
		}
		if into, ok := merged[id]; ok {
			out = append(out, refusal{a: id, b: into, closed: ev.At})
			delete(merged, id)
		}
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
