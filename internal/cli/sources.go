package cli

import (
	"context"
	"strings"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/ghcli"
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

// resolvers are the codec capabilities that look an address up, by
// provider.
func (e *Env) resolvers() map[unit.ProviderID]unit.Resolver {
	return map[unit.ProviderID]unit.Resolver{dslack.Provider: slackResolver{e}}
}

// slackResolver resolves a Slack message permalink to its thread.
type slackResolver struct{ e *Env }

func (s slackResolver) Resolve(ctx context.Context, address string) (string, bool, error) {
	if !slacksource.IsPermalink(address) {
		return "", false, nil
	}
	u, err := s.e.slackPermalinkUnit(ctx, address)
	return u.Key, err == nil, err
}

// locators are the codec capabilities that know the unit a directory
// holds, by provider.
func (e *Env) locators() map[unit.ProviderID]unit.Locator {
	return map[unit.ProviderID]unit.Locator{unit.GitHub: ghLocator{e}}
}

// ghLocator is the pull request of the checkout's branch, as gh reads it.
type ghLocator struct{ e *Env }

func (g ghLocator) UnitAt(ctx context.Context, dir string) (string, bool, error) {
	out, err := ghcli.RunIn(ctx, g.e.GH, dir, "pr", "view", "--json", "url", "--jq", ".url")
	if err != nil {
		return "", false, err
	}
	ref, err := pr.ParseURL(strings.TrimSpace(string(out)))
	if err != nil {
		return "", false, err
	}
	return ref.Unit().Key, true, nil
}
