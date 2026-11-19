package daemon

import (
	"cmp"
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// draftViewFacts are the facts of d a draft facet reads; publisher is the plugin that posts it, and where and
// people come from the draft's unit and its plugin.
func draftViewFacts(d draft.Draft, publisher unit.ProviderID, where string, people []string) workflow.Facts {
	return workflow.Facts{
		"draft.proposer":   d.Origin.Proposer(),
		"draft.workflow":   d.Origin.Workflow,
		"draft.kind":       string(d.Kind),
		"draft.delivery":   string(d.Delivery),
		"draft.provider":   string(publisher),
		"draft.state":      string(d.State),
		"draft.where":      where,
		"draft.people":     append([]string{}, people...),
		"draft.created_at": d.CreatedAt,
	}
}

// draftWhere is where a draft on u is: the first value u's row gives the facet whose id is that of the draft
// facet reading draft.where, else, for a task's unit, the first its task's facet of that id has; "" for none.
func (ev *taskViewer) draftWhere(ctx context.Context, defs []intentload.FacetDef, u unit.Unit, row map[string][]string) (string, error) {
	i := slices.IndexFunc(defs, func(f intentload.FacetDef) bool { return f.Fact == "draft.where" })
	if i < 0 {
		return "", nil
	}
	id := defs[i].ID
	if vs := row[id]; len(vs) > 0 {
		return vs[0], nil
	}
	tid, ok := task.IDOf(u)
	if !ok || ev.g == nil {
		return "", nil
	}
	e, ok, err := ev.taskOf(ctx, tid)
	if err != nil || !ok {
		return "", err
	}
	var v api.TaskView
	if err := ev.row(ctx, e, &v); err != nil {
		return "", err
	}
	ev.facets(ctx, e, &v)
	if vs := v.Facets[id]; len(vs) > 0 {
		return vs[0], nil
	}
	return "", nil
}

// mentioned are the people text mentions, each once: the name names gives a mention key, else the key.
func mentioned(text string, names map[string]string) []string {
	var out []string
	for _, m := range mentionKey.FindAllStringSubmatch(text, -1) {
		if p := cmp.Or(names[m[1]], m[1]); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}
