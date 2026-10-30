package intent

import (
	"errors"
	"io/fs"
	"maps"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// UIFile is the file of a plugin's profile layer that words the views,
// facets, situations and menu bar sections its manifest declares.
const UIFile = "ui.toml"

// UITexts are one plugin's words of its manifest's ids.
type UITexts struct {
	Views      map[string]UIView  `toml:"views"`
	Facets     map[string]UIFacet `toml:"facets"`
	Situations map[string]string  `toml:"situations"`
	Events     map[string]UIEvent `toml:"events"`
	Menubar    map[string]string  `toml:"menubar"`
	Detail     map[string]UIField `toml:"detail"`
}

// UIField is how a field of a unit's detail shows.
type UIField struct {
	Label string `toml:"label"`
	Title string `toml:"title"`
}

// UISearch is a custom view's search: the queries it runs at a press, the
// terms it toggles, the orders it offers and how a result's decision reads.
type UISearch struct {
	DefaultSort string            `toml:"default_sort"`
	Presets     []UISearchPreset  `toml:"presets"`
	Filters     []UISearchFilter  `toml:"filters"`
	Sorts       []UISearchSort    `toml:"sorts"`
	Decisions   map[string]string `toml:"decisions"`
	NoDecision  string            `toml:"no_decision"`
}

// UISearchPreset is a query run at a press.
type UISearchPreset struct {
	Query string `toml:"query"`
	Label string `toml:"label"`
}

// UISearchFilter toggles a term, or with Window a recent-activity window in
// days.
type UISearchFilter struct {
	Term   string `toml:"term"`
	Window int    `toml:"window"`
	Label  string `toml:"label"`
}

// UISearchSort is an order a search offers.
type UISearchSort struct {
	Sort  string `toml:"sort"`
	Label string `toml:"label"`
	Shown string `toml:"shown"`
}

// UIView is a view's words.
type UIView struct {
	Label  string    `toml:"label"`
	Unit   string    `toml:"unit"`
	Search *UISearch `toml:"search"`
}

// UIEvent is an event kind's words.
type UIEvent struct {
	Label string `toml:"label"`
	Loop  bool   `toml:"loop"`
	Data  string `toml:"data"`
}

// UIFacet is a facet's words.
type UIFacet struct {
	Label   string            `toml:"label"`
	Empty   string            `toml:"empty"`
	Values  map[string]string `toml:"values"`
	Buckets map[string]string `toml:"buckets"`
}

// uiPlugin is the plugin whose words layer l holds: its own layer or the
// user's layer of it; "" for a layer of no plugin.
func uiPlugin(l profile.Layer) string {
	if l.Plugin() {
		return strings.TrimPrefix(l.Name, profile.PluginLayer(""))
	}
	id, ok := strings.CutPrefix(l.Name, "user:")
	if !ok {
		return ""
	}
	return id
}

// loadUI reads ui.toml from each plugin's layers, by plugin id: a higher
// layer's text replaces a lower one's, key by key.
func loadUI(layers []profile.Layer) (map[string]UITexts, Problems) {
	out := map[string]UITexts{}
	var problems Problems
	for _, l := range layers {
		id := uiPlugin(l)
		if id == "" {
			continue
		}
		r, err := profile.ReadFile(l, UIFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: r.Path, Msg: err.Error()})
			continue
		}
		m, ps, _, complete := decode[UITexts](r.Path, r.Body)
		problems = append(problems, ps...)
		if !complete {
			continue
		}
		s := &sheet{file: r.Path, at: positions(r.Body)}
		s.addGrammar(grammar(builtin.SurfaceUI, r.Body))
		if problems = append(problems, s.problems...); len(s.problems) > 0 {
			continue
		}
		out[id] = mergeUI(out[id], m)
	}
	return out, problems
}

func mergeUI(into, over UITexts) UITexts {
	into.Views = mergeMap(into.Views, over.Views, func(a, b UIView) UIView {
		if b.Label != "" {
			a.Label = b.Label
		}
		if b.Unit != "" {
			a.Unit = b.Unit
		}
		if b.Search != nil {
			a.Search = b.Search
		}
		return a
	})
	into.Facets = mergeMap(into.Facets, over.Facets, func(a, b UIFacet) UIFacet {
		if b.Label != "" {
			a.Label = b.Label
		}
		if b.Empty != "" {
			a.Empty = b.Empty
		}
		a.Values = mergeMap(a.Values, b.Values, func(_, y string) string { return y })
		a.Buckets = mergeMap(a.Buckets, b.Buckets, func(_, y string) string { return y })
		return a
	})
	into.Events = mergeMap(into.Events, over.Events, func(_, y UIEvent) UIEvent { return y })
	into.Situations = mergeMap(into.Situations, over.Situations, func(_, y string) string { return y })
	into.Detail = mergeMap(into.Detail, over.Detail, func(_, y UIField) UIField { return y })
	into.Menubar = mergeMap(into.Menubar, over.Menubar, func(_, y string) string { return y })
	return into
}

func mergeMap[V any](into, over map[string]V, merge func(a, b V) V) map[string]V {
	if len(over) == 0 {
		return into
	}
	out := maps.Clone(into)
	if out == nil {
		out = map[string]V{}
	}
	for k, v := range over {
		out[k] = merge(out[k], v)
	}
	return out
}

// BundledUI is the words of the bundled plugins' own layers alone.
func BundledUI() (map[string]UITexts, Problems) { return loadUI(profile.BundledChain()) }
