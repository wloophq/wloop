package daemon

import (
	"net/http"

	"github.com/wloophq/wloop/internal/api"
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
	}
	return b.cat
}
