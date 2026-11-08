package automation

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// withdraw discards every proposed draft whose applying workflow's
// [drafts.withdraw] when matches it on fresh facts, with the workflow's reason,
// every draft of a workflow no longer loaded as moot, and every draft of a
// workflow the Keep skips on its subject as skipped; a draft of a
// disabled plugin's unit, of a workflow that failed to load or of one the
// configuration turns off, is left as it is.
func (e *Engine) withdraw(ctx context.Context) error {
	if e.Withdrawals == nil {
		return nil
	}
	live := e.live()
	var kept []string
	if e.Broken != nil {
		kept = e.Broken()
	}
	if e.Disabled != nil {
		kept = append(kept, e.Disabled()...)
	}
	vars := workflow.Vars{Now: e.now()}
	if e.Config != nil {
		vars.Config = e.Config()
	}
	if open, err := e.Withdrawals.Any(ctx); err != nil || !open {
		return err
	}
	judged := map[int64]bool{}
	var errs []error
	for {
		var d ProposedDraft
		var reason string
		err := e.prepared(ctx, func(ctx context.Context) error {
			ds, err := e.Withdrawals.Proposed(ctx, "")
			if err != nil {
				return err
			}
			for _, c := range ds {
				if judged[c.ID] || c.Workflow == "" || e.PluginEnabled != nil && !e.PluginEnabled(c.Subject.Provider) || slices.Contains(kept, c.Workflow) {
					continue
				}
				judged[c.ID] = true
				var err error
				if reason, err = e.withdrawal(ctx, live, c, vars); err != nil {
					errs = append(errs, err)
				} else if reason != "" {
					d = c
					return nil
				}
			}
			return nil
		})
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		if reason == "" {
			break
		}
		// A withdrawal moves the facts of the drafts after it, theirs included: they are listed again in a new pass.
		if err := e.Withdrawals.Withdraw(ctx, d.ID, reason); err != nil {
			errs = append(errs, err)
			continue
		}
		e.logf("%s: draft %d on %s withdrawn: %s", d.Workflow, d.ID, d.Subject, reason)
	}
	return errors.Join(errs...)
}

func (e *Engine) withdrawal(ctx context.Context, live map[string]workflow.Workflow, d ProposedDraft, vars workflow.Vars) (string, error) {
	w, ok := live[d.Workflow]
	if !ok {
		return draft.WithdrawMoot, nil
	}
	if skip, err := e.skipped(ctx, d.Workflow, d.Subject); err != nil || skip {
		if skip {
			return draft.WithdrawSkipped, nil
		}
		return "", err
	}
	rules := w.Withdrawals()
	if len(rules) == 0 || w.Observe {
		return "", nil
	}
	facts, err := e.factsFor(ctx, w, d.Subject)
	switch {
	case errors.Is(err, ErrUnitGone):
		return draft.WithdrawMoot, nil
	case err != nil:
		return "", err
	}
	vars.Facts = workflow.Facts{}
	for _, f := range []workflow.Facts{facts, d.Facts} {
		for k, v := range f {
			vars.Facts[k] = v
		}
	}
	for _, r := range rules {
		switch ok, err := match(r.When, vars); {
		case err != nil:
			return "", err
		case ok:
			return cmp.Or(r.Reason, draft.WithdrawMoot), nil
		}
	}
	return "", nil
}
