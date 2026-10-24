package daemon

import "net/http"

// handleCatalog serves the providers' views, facets and situations.
func (s *Server) handleCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.readModel().Catalog())
}
