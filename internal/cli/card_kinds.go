package cli

import (
	"encoding/json"
	"io/fs"
	"path"
	"slices"

	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	bundledplugins "github.com/wloophq/wloop/plugins"
)

// cardKinds are the merged plugins' draft kinds a draft's card reads, with their schemas from the plugins'
// embedded files.
type cardKinds struct{ files fs.FS }

var _ daemon.DraftKinds = cardKinds{}

func (k cardKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	kinds := builtin.PluginDraftKinds[unit.ProviderID(provider)]
	i := slices.IndexFunc(kinds.Kinds, func(dk manifest.DraftKind) bool { return dk.Name == kind })
	if i < 0 {
		return draft.KindSpec{}, false
	}
	dk := kinds.Kinds[i]
	return draft.KindSpec{Provider: provider, Name: dk.Name, Delivery: dk.Delivery, Schema: dk.Schema, Preview: dk.Preview, Basis: dk.Basis,
		TextRequired: dk.TextRequired, Editable: slices.Clone(dk.Editable)}, true
}

func (k cardKinds) Schema(spec draft.KindSpec) (json.RawMessage, error) {
	if spec.Schema == "" {
		return nil, fs.ErrNotExist
	}
	return fs.ReadFile(k.files, path.Join(spec.Provider, spec.Schema))
}

func manifestKinds() cardKinds { return cardKinds{files: bundledplugins.Manifests} }
