package source

import (
	"cmp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// KeyNav is the action of a [source.ui] key that jumps to the plugin's
// views.
const KeyNav = "nav"

// Catalog is a source plugin's views, facets and situations, and what its
// clients show of it, as its manifest declares them and its profile layer's
// ui.toml words them; an id ui.toml does not word shows as itself.
type Catalog struct {
	Source *manifest.Source
	// Texts are the plugin's words as the profile last loaded them; nil
	// words nothing.
	Texts func() intentload.UITexts
}

func (c Catalog) texts() intentload.UITexts {
	if c.Texts == nil {
		return intentload.UITexts{}
	}
	return c.Texts()
}

func (c Catalog) ui() *manifest.UI {
	if c.Source == nil || c.Source.UI == nil {
		return &manifest.UI{}
	}
	return c.Source.UI
}

func (c Catalog) Views() []api.ViewSpec {
	t := c.texts()
	var out []api.ViewSpec
	for _, v := range c.ui().Views {
		w := t.Views[v.ID]
		out = append(out, api.ViewSpec{ID: v.ID, Label: cmp.Or(w.Label, v.ID), Provider: c.Source.Provider, Rows: v.Rows, Order: v.Order,
			Surfaces: slices.Clone(v.Surfaces), Unit: w.Unit, UnitSort: v.UnitSort})
	}
	return out
}

func (c Catalog) Facets() []api.FacetSpec {
	t := c.texts()
	var out []api.FacetSpec
	for _, f := range c.ui().Facets {
		w := t.Facets[f.ID]
		spec := api.FacetSpec{ID: f.ID, Label: cmp.Or(w.Label, f.ID), Provider: c.Source.Provider, Group: f.Group, Sort: f.Sort,
			Rank: slices.Clone(f.Rank), Empty: w.Empty, Match: f.Match, Menu: f.Menu}
		if len(w.Values) > 0 {
			spec.Labels = w.Values
		}
		for _, b := range f.Buckets {
			within := ""
			if b.Within != 0 {
				within = durationText(time.Duration(b.Within))
			}
			spec.Buckets = append(spec.Buckets, api.Bucket{ID: b.ID, Label: cmp.Or(w.Buckets[b.ID], b.ID), Within: within})
		}
		out = append(out, spec)
	}
	return out
}

func (c Catalog) Situations() []api.SituationSpec {
	if c.Source == nil {
		return nil
	}
	t := c.texts()
	var out []api.SituationSpec
	for i, s := range c.Source.Situations {
		rank := s.Rank
		if rank == 0 {
			rank = i
		}
		out = append(out, api.SituationSpec{Provider: c.Source.Provider, Kind: api.Kind(s.Name), Label: cmp.Or(t.Situations[s.Name], s.Name), Rank: rank})
	}
	return out
}

// Events are the words of the event kinds the manifest declares that its
// profile layer words; a kind it leaves out shows as itself.
func (c Catalog) Events() []api.EventSpec {
	if c.Source == nil {
		return nil
	}
	t := c.texts()
	var out []api.EventSpec
	for _, e := range c.Source.Events {
		if w, ok := t.Events[e.Name]; ok {
			out = append(out, api.EventSpec{Provider: c.Source.Provider, Kind: e.Name, Label: w.Label, Loop: w.Loop})
		}
	}
	return out
}

// PluginUI is what the plugin's clients show of it: a saved view per view,
// the menu bar's sections, its keys, the key that jumps to its views and
// its web directory; nil for a plugin whose manifest declares no
// [source.ui].
func (c Catalog) PluginUI() *api.PluginUI {
	if c.Source == nil || c.Source.UI == nil {
		return nil
	}
	ui := c.Source.UI
	t := c.texts()
	out := &api.PluginUI{}
	for _, v := range c.Views() {
		out.Views = append(out.Views, api.SavedView{ID: v.ID, Label: v.Label, View: v.ID})
	}
	for _, m := range ui.Menubar {
		out.Menubar = append(out.Menubar, api.MenubarSection{Label: cmp.Or(t.Menubar[m.ID], m.ID), View: m.View})
	}
	for _, k := range ui.Keys {
		if k.Action == KeyNav {
			out.Key = k.Key
			continue
		}
		out.Keys = append(out.Keys, api.KeyBinding{Key: k.Key, Action: k.Action, Surface: k.Surface})
	}
	return out
}

// durationText is d as the cockpit's buckets spell it: whole hours as
// hours, else Go's spelling.
func durationText(d time.Duration) string {
	if d != 0 && d%time.Hour == 0 {
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	}
	return d.String()
}

// BundledCatalog is bundled plugin id's catalog, worded by its own layer
// alone; one with no bundled manifest has none.
func BundledCatalog(id string) Catalog {
	srcs, err := bundled.Sources()
	if err != nil {
		return Catalog{}
	}
	i := slices.IndexFunc(srcs, func(s *manifest.Source) bool { return s.Provider == id })
	if i < 0 {
		return Catalog{}
	}
	texts := bundledTexts()
	return Catalog{Source: srcs[i], Texts: func() intentload.UITexts { return texts[id] }}
}

var bundledTexts = sync.OnceValue(func() map[string]intentload.UITexts {
	t, _ := intentload.BundledUI()
	return t
})
