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
			Evidence: &items.Evidence{Kind: items.EvidenceUnitEvent, Anchor: at}, Reason: "workflow " + c.why.Workflow}
	}
	if _, err := s.d.Items.Apply(ctx, items.Workflow, ops); err != nil {
		return "", err
	}
	id, _ := task.IDOf(c.u)
	return fmt.Sprintf("resolved %d items of %s as %s", len(ops), id, res), nil
}

// openItemsOf are the open items the task c runs on holds, or why it holds none the step can resolve.
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
