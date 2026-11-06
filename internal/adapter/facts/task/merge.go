package facttask

import (
	"context"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

const passCostly = "facts.costly."

// mergeFacts sets in f task.clarified, which a merge reads.
func (p *Provider) mergeFacts(ctx context.Context, e task.Task, f workflow.Facts) error {
	clarified, err := p.clarified(ctx, e)
	if err != nil {
		return err
	}
	f["task.clarified"] = clarified
	return nil
}

// costlyFacts sets in f every task fact builtin.Facts marks costly to workflow.Unrequested, but those a reader
// ctx carries names, each computed once per pass for e as it is: task.refused_pairs, task.undone,
// task.link_target_units, and task.link_target_tasks, task.candidates and task.linked_candidates without the refused ids.
func (p *Provider) costlyFacts(ctx context.Context, e task.Task, f workflow.Facts) error {
	wanted := map[string]bool{}
	for _, d := range builtin.Facts {
		if d.Costly && d.Slot == builtin.TaskSlot {
			if workflow.Names(ctx, d.Name) {
				wanted[d.Name] = true
			} else {
				f[d.Name] = workflow.Unrequested{Fact: d.Name}
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	once := func(name string, read func() ([]string, error)) ([]string, error) {
		return coretasks.Once(ctx, passCostly+name+"."+fingerprint(e), read)
	}
	refused, err := once("task.refused_pairs", func() ([]string, error) { return p.refusedPairs(ctx, e) })
	if err != nil {
		return err
	}
	rank := func() (ranked, error) {
		return coretasks.Once(ctx, passCostly+"candidates."+fingerprint(e), func() (ranked, error) { return p.candidates(ctx, e, refused) })
	}
	reads := map[string]func() ([]string, error){
		"task.refused_pairs":     func() ([]string, error) { return refused, nil },
		"task.undone":            func() ([]string, error) { return p.undone(ctx, e) },
		"task.link_target_tasks": func() ([]string, error) { return p.linkTargetTasks(ctx, e, refused) },
		"task.link_target_units": func() ([]string, error) { return p.linkTargetUnits(ctx, e) },
		"task.candidates":        func() ([]string, error) { r, err := rank(); return r.all, err },
		"task.linked_candidates": func() ([]string, error) { r, err := rank(); return r.linked, err },
	}
	for name, read := range reads {
		if !wanted[name] {
			continue
		}
		v, err := once(name, read)
		if err != nil {
			return err
		}
		f[name] = v
	}
	return nil
}
