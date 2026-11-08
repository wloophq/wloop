package daemon

import (
	"net/http"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// handleCatalog serves the providers' views, facets and situations.
func (s *Server) handleCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.rowsCatalog())
}

// rowsCatalog is the read model's catalog, built once per model and loaded profile: the GitHub rows' facets
// read the profile's brief extras.
func (s *Server) rowsCatalog() api.CatalogResponse {
	m, l := s.readModel(), s.loaded()
	b := &s.rowsBuilt
	b.Lock()
	defer b.Unlock()
	if b.model != m || b.loaded != l {
		b.model, b.loaded, b.cat = m, l, m.Catalog()
		b.cat.Events = append(slices.Clone(b.cat.Events), coreEvents(l)...)
	}
	return b.cat
}

// coreEvents are the core's event kinds as the profile's timeline.toml words and tones them, with no provider.
func coreEvents(l *intentload.Loaded) []api.EventSpec {
	var out []api.EventSpec
	for _, k := range builtin.EventKinds {
		if e, ok := l.Timeline.Events[k.Name]; ok {
			out = append(out, api.EventSpec{Kind: k.Name, Label: e.Label, Loop: e.Tone == "loop", Tone: e.Tone})
		}
	}
	return out
}
