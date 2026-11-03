package automation

import (
	"context"
	"errors"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// withdraw discards every proposed draft whose applying workflow's
// [drafts.withdraw] when matches it on fresh facts, with the workflow's reason,
// and every draft of a workflow no longer loaded as moot; a draft of a
// disabled plugin's unit, or of a workflow that failed to load, is left as
// it is.
func (e *Engine) withdraw(ctx context.Context) error {
	if e.Withdrawals == nil {
		return nil
	}
	live := e.live()
	var broken []string
	if e.Broken != nil {
		broken = e.Broken()
	}
	vars := workflow.Vars{Now: e.now()}
	if e.Config != nil {
		vars.Config = e.Config()
	}
	var ds []ProposedDraft
	var errs []error
	for listed := false; !listed || len(ds) > 0; {
		var d ProposedDraft
		var reason string
		err := e.prepared(ctx, func(ctx context.Context) error {
			if !listed {
				var err error
				if ds, err = e.Withdrawals.Proposed(ctx, ""); err != nil {
					return err
				}
				listed = true
				ds = slices.DeleteFunc(ds, func(d ProposedDraft) bool {
					return d.Workflow == "" || e.PluginEnabled != nil && !e.PluginEnabled(d.Subject.Provider) || slices.Contains(broken, d.Workflow)
				})
			}
			for len(ds) > 0 {
				d, ds = ds[0], ds[1:]
				var err error
				if reason, err = e.withdrawal(ctx, live, d, vars); err != nil {
					errs = append(errs, err)
				} else if reason != "" {
					return nil
				}
			}
			return nil
		})
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		if reason == "" {
			continue
		}
		// A withdrawal moves the facts of the drafts after it: they are read in a new pass.
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
	switch {
	case !ok:
		return draft.WithdrawMoot, nil
	case w.Withdraw == nil || w.Observe:
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
	if ok, err := match(w.Withdraw, vars); err != nil || !ok {
		return "", err
	}
	if w.WithdrawReason != "" {
		return w.WithdrawReason, nil
	}
	return draft.WithdrawMoot, nil
}
