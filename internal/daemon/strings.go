package daemon

import (
	"maps"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

func (s *Server) handleStrings(w http.ResponseWriter, _ *http.Request) {
	l := s.loaded()
	out := api.Strings{Locale: l.Locale, Strings: make(map[string]api.Text, len(l.Strings)), Formats: maps.Clone(l.Formats)}
	for k, f := range l.Strings {
		out.Strings[k] = api.Text(f)
	}
	if out.Formats == nil {
		out.Formats = map[string]string{}
	}
	writeJSON(w, out)
}
