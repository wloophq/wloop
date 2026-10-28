package automation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// paused says whether the pause holds a unit: every unit when on, else a
// unit whose repo fact the pause names.
func (p *planner) paused(w workflow.Workflow, facts workflow.Facts) bool {
	return holds(p.pause, w.Slot, facts)
}

func holds(pause Pause, slot string, facts workflow.Facts) bool {
	if pause.On {
		return true
	}
	repo, _ := facts[slot+".repo"].(string)
	return repo != "" && slices.ContainsFunc(pause.Repos, func(r string) bool { return strings.EqualFold(r, repo) })
}

// limit is the reason a workflow's limits skip one more item on u, or "".
// The ledger counts done runs; the items this evaluation already decided to
// run without the user count too.
func (p *planner) limit(w workflow.Workflow, u unit.Unit) (string, error) {
	l := w.Limits
	planned := p.perRun[w.Name]
	if l.PerRun > 0 && planned >= l.PerRun {
		return fmt.Sprintf("limit per_run %d reached", l.PerRun), nil
	}
	day := startOfDay(p.now)
	if l.PerDay > 0 {
		n, err := p.e.Ledger.Count(p.ctx, w.Name, day, nil)
		if err != nil {
			return "", err
		}
		if n+planned >= l.PerDay {
			return fmt.Sprintf("limit per_day %d reached", l.PerDay), nil
		}
	}
	if l.PerUnitPerDay > 0 {
		n, err := p.e.Ledger.Count(p.ctx, w.Name, day, &u)
		if err != nil {
			return "", err
		}
		if n+p.perUnit[unitOf{w.Name, u}] >= l.PerUnitPerDay {
			return fmt.Sprintf("limit per_unit_per_day %d reached", l.PerUnitPerDay), nil
		}
	}
	if l.Cooldown > 0 {
		n, err := p.e.Ledger.Count(p.ctx, w.Name, p.now.Add(-l.Cooldown), &u)
		if err != nil {
			return "", err
		}
		if n > 0 {
			return fmt.Sprintf("cooling down for %s", l.Cooldown), nil
		}
	}
	return "", nil
}

type unitOf struct {
	workflow string
	u        unit.Unit
}

// runsAlone says whether an item runs without the user: decided run,
// deferred, or pending, not observed, with default run. Only those take a
// slot.
func runsAlone(it workflow.Item) bool {
	return it.Decision == workflow.DecisionRun || it.Decision == workflow.DecisionDeferred ||
		it.Decision == workflow.DecisionPending && it.Default == workflow.OutcomeRun && !it.Observe
}

// count reserves the item's slots in the limits.
func (p *planner) count(it workflow.Item) {
	p.perRun[it.Workflow]++
	p.perUnit[unitOf{it.Workflow, it.Unit}]++
}
