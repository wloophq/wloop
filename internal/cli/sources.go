package cli

import (
	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/source"
)

// plugins are the compiled-in plugins; this is the only file that names
// one.
var plugins = []source.Plugin{{ID: unit.GitHub, Name: "GitHub", Codec: bundledCodec(unit.GitHub), Build: ghsource.Module},
	{ID: dslack.Provider, Name: "Slack", Codec: bundledCodec(dslack.Provider), Build: slackModule}}

// bundledCodec is the codec bundled plugin id's manifest declares.
func bundledCodec(id unit.ProviderID) unit.Codec {
	cs, err := bundled.Codecs()
	if err != nil {
		panic(err)
	}
	return cs[id]
}
