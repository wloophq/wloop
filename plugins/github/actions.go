package github

import (
	"context"
	"encoding/json"
	"fmt"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
)

// The actions the host calls.
const (
	// ActionBasics reads a pull request's basics: its answer is the
	// pull request as JSON.
	ActionBasics = "pr.basics"
	// ActionRefresh reads a pull request in full into the store and
	// reports it.
	ActionRefresh = "pr.refresh"
	// ActionBudget answers the API's budget, its limits as JSON.
	ActionBudget = "github.budget"
	// ActionForward sends an api.Exchange, its args, with the plugin's
	// credentials and answers the api.Answer. Only the host's own code
	// calls it: a person's or an agent's request is refused.
	ActionForward = "github.forward"
)

// Action runs one of the actions the host calls.
func (p *Plugin) Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
	r, err := p.running()
	if err != nil {
		return nil, err
	}
	switch req.Action {
	case ActionBasics:
		ref, err := refOf(req.Unit)
		if err != nil {
			return nil, err
		}
		basics, err := r.client.Basics(ctx, ref)
		if err != nil {
			return nil, err
		}
		return json.Marshal(basics)
	case ActionRefresh:
		ref, err := refOf(req.Unit)
		if err != nil {
			return nil, err
		}
		if err := r.col.Refresh(ctx, ref); err != nil {
			return nil, err
		}
		return nil, r.report(ctx, false)
	case ActionBudget:
		return json.Marshal(r.client.Budget())
	case ActionForward:
		if req.Actor != "" {
			return nil, fmt.Errorf("%w: %s is the host's own", src.ErrUnsupported, req.Action)
		}
		var x api.Exchange
		if err := json.Unmarshal(req.Args, &x); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", src.ErrUnfit, req.Action, err)
		}
		a, err := r.client.Forward(ctx, x)
		if err != nil {
			return nil, err
		}
		return json.Marshal(a)
	}
	return nil, fmt.Errorf("%w: no action %s", src.ErrUnsupported, req.Action)
}
