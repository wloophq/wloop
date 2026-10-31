package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func (s *stepSet) itemsResolveCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	open, refusal, err := s.openItemsOf(ctx, c)
	if err != nil || refusal != "" {
		return refusal, err
	}
	if len(open) == 0 {
		return "the task holds no open item", nil
	}
	return "", nil
}

func (s *stepSet) itemsResolveRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	open, refusal, err := s.openItemsOf(ctx, c)
	if err != nil {
		return "", err
	}
	if refusal == "" && len(open) == 0 {
		refusal = "the task holds no open item"
	}
	if refusal != "" {
		return "", automation.Refused{Reason: refusal}
	}
	res := items.Resolution(c.arg("resolution"))
	if res == "" {
		res = items.Done
	}
	now := s.e.Now()
	ops := make([]items.Op, len(open))
	for i, it := range open {
		at := unit.Anchor{Unit: it.Unit, Ref: "workflow:" + c.why.Workflow, At: now}
		ops[i] = items.Op{Op: items.OpResolve, Unit: it.Unit, Item: it.ID, Anchor: at, Resolution: res,
			Evidence: &items.Evidence{Kind: items.EvidenceWorkflow, Anchor: at}, Reason: "workflow " + c.why.Workflow}
	}
	got, err := s.d.Items.Apply(ctx, items.Workflow, ops)
	if err != nil {
		return "", err
	}
	if len(got.Refused) > 0 {
		return "", automation.Refused{Reason: fmt.Sprintf("the ledger refused %d of %d items: %s", len(got.Refused), len(ops), got.Refused[0].Reason)}
	}
	resolved := 0
	for _, a := range got.Applied {
		if a.Changed && a.Item.State == items.Resolved {
			resolved++
		}
	}
	id, _ := task.IDOf(c.u)
	out := fmt.Sprintf("resolved %d items of %s as %s", resolved, id, res)
	if len(got.Proposed) > 0 {
		out += fmt.Sprintf(", proposed %d you confirmed", len(got.Proposed))
	}
	return out, nil
}

// openItemsOf are the open items the derived task c runs on holds, or why it holds none the step can resolve.
func (s *stepSet) openItemsOf(ctx context.Context, c stepCall) ([]items.Item, string, error) {
	if s.d.Items == nil {
		return nil, "", errors.New("no items ledger: the daemon runs the steps")
	}
	g, err := s.tasks()
	if err != nil {
		return nil, "", err
	}
	id, ok := task.IDOf(c.u)
	if !ok {
		return nil, "", fmt.Errorf("%s: %s is not a task", c.st.Use, c.u)
	}
	e, err := g.Task(ctx, id)
	if err != nil {
		if r := taskRefusal(err); r != "" {
			return nil, r, nil
		}
		return nil, "", err
	}
	if e.Origin != task.OriginDerived {
		return nil, "the task is not derived from items: its items do not close it", nil
	}
	held := map[unit.Unit]map[string]bool{}
	for _, r := range e.Items {
		if held[r.Unit] == nil {
			held[r.Unit] = map[string]bool{}
		}
		held[r.Unit][r.Item] = true
	}
	var out []items.Item
	for _, u := range e.Units {
		if held[u] == nil {
			continue
		}
		its, err := s.d.Items.Items(ctx, u)
		if err != nil {
			return nil, "", err
		}
		for _, it := range its {
			if held[u][it.ID] && it.State == items.Open {
				out = append(out, it)
			}
		}
	}
	return out, "", nil
}
