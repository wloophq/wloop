package intent

import (
	"errors"
	"io/fs"
	"maps"
	"strings"

	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/i18n"
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
	Reasons    map[string]string  `toml:"reasons"`
	Events     map[string]UIEvent `toml:"events"`
	Menubar    map[string]string  `toml:"menubar"`
	Detail     map[string]UIField `toml:"detail"`
	LensRaw    map[string]string  `toml:"lens"`
	// Lens are the plugin's own row words, those it leaves out empty.
	Lens    RowWords `toml:"-"`
	NounRaw any      `toml:"noun"`
	// Noun is what one of the plugin's units is, as the briefing counts it.
	Noun i18n.Forms `toml:"-"`
	// Decisions are the plugin's words of the decisions its units wait on, by decision kind.
	Decisions map[string]UIDecision `toml:"decisions"`
}

// UIDecision is a plugin's words of a decision kind.
type UIDecision struct {
	ClauseRaw any        `toml:"clause"`
	Clause    i18n.Forms `toml:"-"`
	Queue     string     `toml:"queue"`
	Empty     string     `toml:"empty"`
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
	Label       string    `toml:"label"`
	Unit        string    `toml:"unit"`
	DefaultSort string    `toml:"default_sort"`
	Search      *UISearch `toml:"search"`
}

// UIEvent is an event kind's words.
type UIEvent struct {
	Label string `toml:"label"`
	Loop  bool   `toml:"loop"`
	Tone  string `toml:"tone"`
	Data  string `toml:"data"`
}

// UIFacet is a facet's words.
type UIFacet struct {
	Label       string              `toml:"label"`
	Empty       string              `toml:"empty"`
	Values      map[string]string   `toml:"values"`
	Buckets     map[string]string   `toml:"buckets"`
	ShowRaw     any                 `toml:"show"`
	ShowValues  *string             `toml:"show_values"`
	ShowWhen    map[string][]string `toml:"show_when"`
	ShowMax     *int                `toml:"show_max"`
	ShowLabel   *string             `toml:"show_label"`
	LabelPrefix *string             `toml:"label_prefix"`
	RowClass    map[string]string   `toml:"row_class"`
	Colors      map[string]string   `toml:"colors"`
	// Show is where the facet's values show on the plugin's rows, read from ShowRaw and ShowValues.
	Show Show `toml:"-"`
}

func (f UIFacet) placement() placement {
	return placement{When: f.ShowWhen, Max: f.ShowMax, Label: f.ShowLabel}
}

// loadUI reads ui.toml from each plugin's layers, by plugin id: a higher
// layer's text replaces a lower one's, key by key.
func loadUI(layers []profile.Layer, r layerReader) (map[string]UITexts, Problems) {
	out := map[string]UITexts{}
	var problems Problems
	for _, l := range layers {
		id := l.PluginID()
		if id == "" {
			continue
		}
		res, err := r.read(l, UIFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, Problem{File: res.Path, Msg: err.Error()})
			continue
		}
		m, ps, _, complete := decode[UITexts](res.Path, res.Body)
		problems = append(problems, ps...)
		if !complete {
			continue
		}
		s := &sheet{file: res.Path, at: positions(res.Body), funcs: r.funcs()}
		s.addGrammar(grammar(builtin.SurfaceUI, res.Body))
		for id, f := range m.Facets {
			if f.ShowRaw != nil || f.ShowValues != nil {
				readShow(s, "facets."+id, f.ShowRaw, f.ShowValues, &f.Show)
			}
			readPlacement(s, "facets."+id, f.placement(), &f.Show)
			m.Facets[id] = f
		}
		mergeRowWords(s, "lens", m.LensRaw, &m.Lens)
		if m.NounRaw != nil {
			m.Noun, _ = formsOf(s, "noun", m.NounRaw)
		}
		for k, d := range m.Decisions {
			if !decision.Valid(decision.Kind(k)) {
				continue
			}
			if d.ClauseRaw != nil {
				d.Clause, _ = formsOf(s, "decisions."+k+".clause", d.ClauseRaw)
			}
			m.Decisions[k] = d
		}
		if problems = append(problems, s.problems...); len(s.problems) > 0 {
			continue
		}
		out[id] = mergeUI(out[id], m)
	}
	if r.texts != nil {
		for id, u := range out {
			u.Noun = pluginForms(r, id, u.Noun, "noun")
			for k, d := range u.Decisions {
				d.Clause = pluginForms(r, id, d.Clause, "decisions", k, "clause")
				u.Decisions[k] = d
			}
			out[id] = u
		}
	}
	return out, problems
}

// pluginForms is the plural text at path of plugin id's ui.toml in r's locale, else in.
func pluginForms(r layerReader, id string, in i18n.Forms, path ...string) i18n.Forms {
	if in == nil {
		return nil
	}
	if f, ok := r.texts.PluginForms(id, UIFile, path); ok && strings.TrimSpace(f["other"]) != "" {
		return f
	}
	return in
}

func mergeUI(into, over UITexts) UITexts {
	into.Views = mergeMap(into.Views, over.Views, func(a, b UIView) UIView {
		if b.Label != "" {
			a.Label = b.Label
		}
		if b.Unit != "" {
			a.Unit = b.Unit
		}
		if b.DefaultSort != "" {
			a.DefaultSort = b.DefaultSort
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
		a.RowClass = mergeMap(a.RowClass, b.RowClass, func(_, y string) string { return y })
		a.Colors = mergeMap(a.Colors, b.Colors, func(_, y string) string { return y })
		if b.ShowRaw != nil {
			a.ShowRaw, a.Show.Part, a.Show.Only = b.ShowRaw, b.Show.Part, b.Show.Only
		}
		if b.ShowValues != nil {
			a.ShowValues, a.Show.Values = b.ShowValues, b.Show.Values
		}
		if b.LabelPrefix != nil {
			a.LabelPrefix = b.LabelPrefix
		}
		a.Show = a.Show.over(b.placement(), b.Show)
		if b.ShowWhen != nil {
			a.ShowWhen = b.ShowWhen
		}
		if b.ShowMax != nil {
			a.ShowMax = b.ShowMax
		}
		if b.ShowLabel != nil {
			a.ShowLabel = b.ShowLabel
		}
		return a
	})
	into.Lens = into.Lens.Over(over.Lens)
	into.Events = mergeMap(into.Events, over.Events, func(_, y UIEvent) UIEvent { return y })
	into.Situations = mergeMap(into.Situations, over.Situations, func(_, y string) string { return y })
	into.Reasons = mergeMap(into.Reasons, over.Reasons, func(_, y string) string { return y })
	into.Detail = mergeMap(into.Detail, over.Detail, func(_, y UIField) UIField { return y })
	into.Menubar = mergeMap(into.Menubar, over.Menubar, func(_, y string) string { return y })
	if over.Noun != nil {
		into.Noun = over.Noun
	}
	into.Decisions = mergeMap(into.Decisions, over.Decisions, func(a, b UIDecision) UIDecision {
		if b.Clause != nil {
			a.Clause = b.Clause
		}
		if b.Queue != "" {
			a.Queue = b.Queue
		}
		if b.Empty != "" {
			a.Empty = b.Empty
		}
		return a
	})
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
func BundledUI() (map[string]UITexts, Problems) { return loadUI(profile.BundledChain(), readerOf(nil)) }
