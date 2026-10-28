package daemon

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/keys"
)

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	surface := keys.Surface(r.URL.Query().Get("surface"))
	if !slices.Contains([]keys.Surface{keys.Web, keys.TUI, keys.Menubar}, surface) {
		writeError(w, http.StatusBadRequest, "surface is web, tui or menubar")
		return
	}
	l := s.loaded()
	writeJSON(w, api.KeysResponse{Bindings: keyViews(surface, s.servedSpecs(), catalog.PlanSpecs(l), coreWords{l.Keys, l.Lists, l.Tasks})})
}

// coreWords are the profile's words for the core keys: their help, and a
// verb's summary.
type coreWords struct {
	keys  intentload.KeyTexts
	lists intentload.ListTexts
	tasks intentload.TasksDef
}

func keyViews(surface keys.Surface, specs, plan []api.ActionSpec, words coreWords) []api.KeyView {
	bundled := bundledWords()
	out := []api.KeyView{}
	for _, b := range keys.Core {
		if slices.Contains(b.Surfaces, surface) {
			help := cmp.Or(words.keys.Help(string(b.Context), b.Action), bundled.keys.Help(string(b.Context), b.Action), b.Action)
			var summary string
			if b.Context == keys.List {
				summary = cmp.Or(words.tasks.Verbs[b.Action].Summary, bundled.tasks.Verbs[b.Action].Summary)
			}
			out = append(out, api.KeyView{Key: b.Key, Action: b.Action, Context: string(b.Context), Owner: "core", Help: help, Summary: summary})
		}
	}
	add := func(specs []api.ActionSpec, ctx keys.Context) {
		for _, s := range specs {
			if s.Key != "" && slices.Contains(keys.SurfacesOf(s.Surfaces), surface) {
				out = append(out, api.KeyView{Key: s.Key, Action: s.ID, Context: string(ctx), Owner: keyOwner(s), Help: s.Label, Summary: s.Summary})
			}
		}
	}
	add(specs, keys.List)
	add(plan, keys.Plan)
	return out
}

func keyOwner(s api.ActionSpec) string {
	if s.Source == catalog.SourceBuiltin {
		return cmp.Or(s.Plugin, "core")
	}
	return "intent:" + s.ID
}
