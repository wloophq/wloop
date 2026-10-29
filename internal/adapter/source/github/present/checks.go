package present

import (
	"context"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// checks are the failing checks of a pull request the inventory holds,
// with the failed steps of their GitHub Actions jobs, as the GitHub plugin
// reads them.
func (p *Presenter) checks(ctx context.Context, u unit.Unit) ([]api.CheckDetail, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return nil, api.Refuse(http.StatusNotFound, "%s is not in the inventory", u.Label())
	}
	out := []api.CheckDetail{}
	err := p.pluginView(ctx, pluginViewChecks, map[string]any{"owner": ref.Owner, "repo": ref.Repo, "number": ref.Number}, &out)
	return out, err
}
