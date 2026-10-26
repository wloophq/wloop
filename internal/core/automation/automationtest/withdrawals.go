package automationtest

import (
	"context"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/automation"
)

// Withdrawals is the open drafts of each workflow, as Put leaves them;
// Withdraw drops a draft and records the reason.
type Withdrawals struct {
	mu        sync.Mutex
	open      map[string][]automation.ProposedDraft
	withdrawn map[int64]string
}

var _ automation.Withdrawals = (*Withdrawals)(nil)

func NewWithdrawals() *Withdrawals {
	return &Withdrawals{open: map[string][]automation.ProposedDraft{}, withdrawn: map[int64]string{}}
}

// Put adds open drafts of workflow, setting their Workflow.
func (w *Withdrawals) Put(workflow string, ds ...automation.ProposedDraft) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, d := range ds {
		d.Workflow = workflow
		w.open[workflow] = append(w.open[workflow], d)
	}
}

// Proposed lists the drafts of workflow, every workflow's by id when it is "".
func (w *Withdrawals) Proposed(_ context.Context, workflow string) ([]automation.ProposedDraft, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if workflow != "" {
		return slices.Clone(w.open[workflow]), nil
	}
	var out []automation.ProposedDraft
	for _, ds := range w.open {
		out = append(out, ds...)
	}
	slices.SortFunc(out, func(a, b automation.ProposedDraft) int { return int(a.ID - b.ID) })
	return out, nil
}

func (w *Withdrawals) Withdraw(_ context.Context, id int64, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for wf, ds := range w.open {
		w.open[wf] = slices.DeleteFunc(ds, func(d automation.ProposedDraft) bool { return d.ID == id })
	}
	w.withdrawn[id] = reason
	return nil
}

// Withdrawn is the reason draft id was withdrawn with; ok is false when it
// was not.
func (w *Withdrawals) Withdrawn(id int64) (reason string, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	reason, ok = w.withdrawn[id]
	return reason, ok
}
