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
	var views []api.TaskViewSpec
	if s.deps.Tasks != nil {
		views = s.servedViews(s.deps.Tasks)
	}
	writeJSON(w, api.KeysResponse{Bindings: keyViews(surface, views, s.deps.Plugins, s.servedSpecs(), catalog.PlanSpecs(l), coreWords{l.Keys, l.Lists, l.Tasks})})
}

// coreWords are the profile's words for the core keys: their help, and a
// verb's summary.
type coreWords struct {
	keys  intentload.KeyTexts
	lists intentload.ListTexts
	tasks intentload.TasksDef
}

// keyViews are the core keys, the keys of views and of the plugins' views, then the keys of specs and of plan.
// A plugin's key that a view holds is left out.
func keyViews(surface keys.Surface, views []api.TaskViewSpec, plugins []api.Plugin, specs, plan []api.ActionSpec, words coreWords) []api.KeyView {
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
	jumps := make([]keys.View, len(views))
	label := map[string]string{}
	for i, v := range views {
		jumps[i] = keys.View{ID: v.ID, Key: v.Key}
		label["nav:"+v.ID] = v.Label
	}
	for _, b := range keys.Views(jumps) {
		if slices.Contains(b.Surfaces, surface) {
			out = append(out, api.KeyView{Key: b.Key, Action: b.Action, Context: string(b.Context), Owner: "core", Help: label[b.Action]})
		}
	}
	for _, p := range plugins {
		if !p.Enabled || p.UI == nil || p.UI.Key == "" || slices.ContainsFunc(jumps, func(v keys.View) bool { return v.Key == p.UI.Key }) {
			continue
		}
		for _, b := range keys.Views([]keys.View{{ID: string(p.ID), Key: p.UI.Key}}) {
			if slices.Contains(b.Surfaces, surface) {
				out = append(out, api.KeyView{Key: b.Key, Action: b.Action, Context: string(b.Context), Owner: string(p.ID), Help: p.Name})
			}
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
