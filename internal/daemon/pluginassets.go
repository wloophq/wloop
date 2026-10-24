package daemon

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// handlePluginAsset serves a file of an enabled plugin's WebAssets; a
// disabled plugin, or one without assets, answers 404.
func (s *Server) handlePluginAsset(w http.ResponseWriter, r *http.Request) {
	id, name := unit.ProviderID(r.PathValue("id")), r.PathValue("path")
	assets := s.pluginAssets(id)
	if assets == nil || !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	raw, err := fs.ReadFile(assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = http.DetectContentType(raw)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(raw))
}

func (s *Server) pluginAssets(id unit.ProviderID) fs.FS {
	if !s.pluginEnabled(id) {
		return nil
	}
	for _, rt := range s.deps.Runtimes {
		if rt.ID == id {
			return rt.WebAssets
		}
	}
	return nil
}
