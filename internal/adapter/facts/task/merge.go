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
// ctx carries names, each computed once per pass for e as it is: task.refused_merges, and task.link_target_tasks
// and task.candidates without the refused ids.
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
	refused, err := once("task.refused_merges", func() ([]string, error) { return p.refusedMerges(ctx, e) })
	if err != nil {
		return err
	}
	reads := map[string]func() ([]string, error){
		"task.refused_merges":    func() ([]string, error) { return refused, nil },
		"task.link_target_tasks": func() ([]string, error) { return p.linkTargetTasks(ctx, e, refused) },
		"task.candidates":        func() ([]string, error) { return p.candidates(ctx, e, refused) },
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
