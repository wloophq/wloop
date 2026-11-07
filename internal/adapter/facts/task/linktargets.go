package facttask

import (
	"cmp"
	"context"
	"slices"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const passHolders = "facts.link_holders"

// linkTargets are the units a review or merge link of e's items targets, each once, but e's own; none for a task
// that may not be merged.
func (p *Provider) linkTargets(ctx context.Context, e task.Task) ([]unit.Unit, error) {
	if p.d.Candidates.From == nil || !p.mergeable(e) {
		return nil, nil
	}
	own := unitsOf(e)
	var targets []unit.Unit
	err := p.eachLink(ctx, own, true, func(l link.Link) {
		if l.Kind != link.Mention && cites(e, l) && !slices.Contains(own, l.To) && !slices.Contains(targets, l.To) {
			targets = append(targets, l.To)
		}
	})
	return targets, err
}

// linkTargetUnits is task.link_target_units of e: its linkTargets, whatever task holds them or is refused.
func (p *Provider) linkTargetUnits(ctx context.Context, e task.Task) ([]string, error) {
	targets, err := p.linkTargets(ctx, e)
	out := make([]string, len(targets))
	for i, u := range targets {
		out[i] = u.String()
	}
	return out, err
}

// linkTargetTasks is task.link_target_tasks of e, without refused and the tasks related to e: the other
// mergeable tasks holding a unit a review or merge link of e's targets, oldest first.
func (p *Provider) linkTargetTasks(ctx context.Context, e task.Task, refused []string) ([]string, error) {
	out := []string{}
	targets, err := p.linkTargets(ctx, e)
	if err != nil || len(targets) == 0 {
		return out, err
	}
	holders, err := p.holders(ctx)
	if err != nil {
		return nil, err
	}
	related, err := p.related(ctx, e)
	if err != nil {
		return nil, err
	}
	refused = append(slices.Clone(refused), related...)
	var found []task.Task
	for _, u := range targets {
		for _, o := range holders[u] {
			if o.ID != e.ID && !slices.Contains(refused, string(o.ID)) && !slices.ContainsFunc(found, func(f task.Task) bool { return f.ID == o.ID }) {
				found = append(found, o)
			}
		}
	}
	slices.SortFunc(found, func(a, b task.Task) int { return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID)) })
	for _, o := range found {
		out = append(out, string(o.ID))
	}
	return out, nil
}

// holders are the mergeable tasks by the units they hold: a pass's, indexed once, else the stored open ones.
func (p *Provider) holders(ctx context.Context) (map[unit.Unit][]task.Task, error) {
	index := func(es []task.Task) map[unit.Unit][]task.Task {
		out := map[unit.Unit][]task.Task{}
		for _, o := range es {
			if p.mergeable(o) {
				for _, u := range unitsOf(o) {
					out[u] = append(out[u], o)
				}
			}
		}
		return out
	}
	if pass, ok := coretasks.PassOf(ctx); ok {
		return coretasks.Once(ctx, passHolders, func() (map[unit.Unit][]task.Task, error) { return index(pass.Tasks), nil })
	}
	es, err := p.d.Tasks.Tasks(ctx, task.Query{})
	return index(es), err
}
