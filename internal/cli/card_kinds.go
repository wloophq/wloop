package cli

import (
	"encoding/json"
	"io/fs"
	"path"

	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	bundledplugins "github.com/wloophq/wloop/plugins"
)

// cardKinds are the outbox's draft kinds as a draft's card reads them, with each kind's schema read from the
// plugins' embedded files.
type cardKinds struct {
	manifestKinds
	files fs.FS
}

var _ daemon.DraftKinds = cardKinds{}

func (k cardKinds) Schema(spec draft.KindSpec) (json.RawMessage, error) {
	if spec.Schema == "" {
		return nil, fs.ErrNotExist
	}
	return fs.ReadFile(k.files, path.Join(spec.Provider, spec.Schema))
}

// cardDraftKinds are the card's kinds over the installed vocabulary.
func cardDraftKinds() cardKinds { return cardKinds{manifestKinds: draftKinds, files: bundledplugins.Manifests} }
