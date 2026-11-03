package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ItemsLedger is the items ledger as the unit card reads it.
type ItemsLedger interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

func wireItemsResult(r coreitems.Result) api.ItemsResult {
	out := api.ItemsResult{Applied: []api.ItemApplied{}, Proposed: []api.ItemProposal{}, Refused: []api.ItemRefused{}}
	for _, a := range r.Applied {
		out.Applied = append(out.Applied, api.ItemApplied{Op: a.Op, Item: a.Item, Changed: a.Changed})
	}
	for _, p := range r.Proposed {
		out.Proposed = append(out.Proposed, wireProposal(p))
	}
	for _, f := range r.Refused {
		out.Refused = append(out.Refused, api.ItemRefused{Index: f.Index, Op: f.Op, Reason: f.Reason})
	}
	return out
}

func wireProposal(p coreitems.Proposal) api.ItemProposal {
	return api.ItemProposal{ID: p.ID, Unit: p.Unit, Item: p.Item, Op: p.Op, Provenance: p.Provenance,
		Reason: p.Reason, State: string(p.State), At: p.At}
}

// SyncUnits is Deps.TaskUnits over tasks and ledger: the units with an open item or a live task.
func SyncUnits(tasks coretasks.Reader, ledger *coreitems.Ledger) func(context.Context) ([]unit.Unit, error) {
	return func(ctx context.Context) ([]unit.Unit, error) {
		open, err := ledger.Query(ctx, coreitems.Query{State: items.Open})
		if err != nil {
			return nil, err
		}
		seen := map[unit.Unit]bool{}
		var out []unit.Unit
		add := func(u unit.Unit) {
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
		for _, it := range open {
			add(it.Unit)
		}
		live, err := tasks.Tasks(ctx, task.Query{})
		if err != nil {
			return nil, err
		}
		for _, e := range live {
			for _, u := range e.Units {
				add(u)
			}
			for _, r := range e.Items {
				add(r.Unit)
			}
		}
		return out, nil
	}
}
