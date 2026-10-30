package cli

import (
	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
)

// plugins are the compiled-in plugins; this is the only file that names
// one.
var plugins = []compiledPlugin{{ID: unit.GitHub, Name: "GitHub", Codec: bundledCodec(unit.GitHub), Build: githubHostModule},
	{ID: slackPlugin, Name: "Slack", Codec: bundledCodec(slackPlugin), Build: slackModule}}

// bundledCodec is the codec bundled plugin id's manifest declares.
func bundledCodec(id unit.ProviderID) unit.Codec {
	cs, err := bundled.Codecs()
	if err != nil {
		panic(err)
	}
	return cs[id]
}

// workspaceHoldings are the units the workspaces hold, as the GitHub
// plugin's pull requests match them.
func (e *Env) workspaceHoldings() ghsource.Holdings {
	return ghsource.Holdings{PRs: e.planInputs(), Overrides: inventory.ByUnit{Store: e.Store}}
}
