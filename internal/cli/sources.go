package cli

import (
	"context"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
)

// plugins are the compiled-in plugins; this is the only file that names
// one.
var plugins = []source.Plugin{{ID: unit.GitHub, Name: "GitHub", Codec: pr.Codec{}, Build: ghsource.Module}, {ID: dslack.Provider, Name: "Slack", Codec: dslack.Codec(), Build: slackModule}}

// githubSource is the GitHub source a pull request is read through outside
// the jobs: no workspaces, sink or log.
func (e *Env) githubSource(cfg config.Config) *ghsource.Collector {
	return &ghsource.Collector{Store: e.Store, GH: e.GitHub, Cfg: cfg, Now: e.Now}
}

// storedGitHubBudget is the GitHub budget the last sweep kept.
func storedGitHubBudget(ctx context.Context, st *inventory.Store) ([]github.Limit, error) {
	return ghsource.StoredBudget(ctx, st)
}

// githubTeams are the viewer's teams the GitHub source resolved.
func githubTeams(ctx context.Context, st *inventory.Store) ([]string, error) {
	return ghsource.MyTeams(ctx, st)
}
