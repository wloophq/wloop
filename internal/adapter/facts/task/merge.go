package facttask

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// mergeFacts sets in f the facts a merge and its proposals read: task.clarified, task.refused_merges,
// task.link_target_tasks and task.candidates, the last two without the refused ids.
func (p *Provider) mergeFacts(ctx context.Context, e task.Task, f workflow.Facts) error {
	clarified, err := p.clarified(ctx, e)
	if err != nil {
		return err
	}
	refused, err := p.refusedMerges(ctx, e)
	if err != nil {
		return err
	}
	targets, err := p.linkTargetTasks(ctx, e, refused)
	if err != nil {
		return err
	}
	candidates, err := p.candidates(ctx, e, refused)
	if err != nil {
		return err
	}
	f["task.clarified"], f["task.refused_merges"], f["task.link_target_tasks"], f["task.candidates"] = clarified, refused, targets, candidates
	return nil
}
