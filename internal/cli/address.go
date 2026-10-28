package cli

import (
	"context"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/pr"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// addressWorkspace is the author workspace a triage round on ref runs in,
// created by authorWorkspace when the PR, being mine, has none; action says
// what was done, "" when it existed.
func (e *Env) addressWorkspace(ctx context.Context, cfg config.Config, ref pr.Ref, pr *pr.PullRequest, mine func() (bool, error)) (ws wsdomain.Workspace, action string, err error) {
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return ws, "", err
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return ws, "", err
	}
	ws, found, err := findAuthorWorkspace(cfg, repos, all, ref, pr, mine)
	if err != nil || found {
		return ws, "", err
	}
	if ok, err := mine(); err != nil {
		return ws, "", err
	} else if !ok {
		return ws, "", fmt.Errorf("no author workspace for %s; it is not yours: take it over first with '"+brand.Command+" address --take-over %s'", ref.URL(), ref.URL())
	}
	return e.authorWorkspace(ctx, cfg, ref, pr, mine, repos, all)
}
