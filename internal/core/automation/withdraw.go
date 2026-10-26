package automation

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// withdraw discards every proposed draft whose applying workflow's
// [withdraw] when matches it on fresh facts, with the workflow's reason,
// and every draft of a workflow no longer loaded as moot; a draft of a
// disabled plugin's unit is left as it is.
func (e *Engine) withdraw(ctx context.Context) error {
	if e.Withdrawals == nil {
		return nil
	}
	ds, err := e.Withdrawals.Proposed(ctx, "")
	if err != nil {
		return err
	}
	live := e.live()
	vars := workflow.Vars{Now: e.now()}
	if e.Config != nil {
		vars.Config = e.Config()
	}
	var errs []error
	for _, d := range ds {
		if d.Workflow == "" || e.PluginEnabled != nil && !e.PluginEnabled(d.Subject.Provider) {
			continue
		}
		reason, err := e.withdrawal(ctx, live, d, vars)
		if err == nil && reason != "" {
			err = e.Withdrawals.Withdraw(ctx, d.ID, reason)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if reason != "" {
			e.logf("%s: draft %d on %s withdrawn: %s", d.Workflow, d.ID, d.Subject, reason)
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) withdrawal(ctx context.Context, live map[string]workflow.Workflow, d ProposedDraft, vars workflow.Vars) (string, error) {
	w, ok := live[d.Workflow]
	switch {
	case !ok:
		return draft.WithdrawMoot, nil
	case w.Withdraw == nil || w.Mode != workflow.ModeApply:
		return "", nil
	}
	facts, err := e.factsFor(ctx, d.Workflow, d.Subject)
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
