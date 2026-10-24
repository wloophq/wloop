package cli

import (
	"maps"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/turn"
)

// servePayloads registers the turn payload readers of the enabled sources.
func (e *Env) servePayloads(mods []source.Module) {
	e.payloads = map[unit.ProviderID]turn.PayloadReader{}
	for _, m := range mods {
		if m.Payload != nil {
			e.payloads[m.ID] = m.Payload
		}
	}
}

// payloadReaders are github's turn payload reader and the sources' own.
func (e *Env) payloadReaders() map[unit.ProviderID]turn.PayloadReader {
	out := map[unit.ProviderID]turn.PayloadReader{}
	maps.Copy(out, e.payloads)
	out[unit.GitHub] = turn.GitHub{PRs: basicsReader{e}}
	return out
}
