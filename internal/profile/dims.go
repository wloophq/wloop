package profile

import (
	"cmp"
	"slices"
	"strings"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

type Dim string

const (
	DimOrganization Dim = "organization"
	DimRepo         Dim = "repo"
	DimUser         Dim = "user"
	DimTeam         Dim = "team"
	DimAssociation  Dim = "association"
	DimLabel        Dim = "label"
	DimBase         Dim = "base"
	DimStage        Dim = dintent.DimStage
	DimReviewer     Dim = dintent.DimReviewer
	DimKind         Dim = "kind"
	DimSize         Dim = "size"
	DimRisk         Dim = "risk"
)

// Alias binds one of today's dimension names to the fact it reads.
type Alias struct {
	Dim  Dim
	Fact string
}

// Aliases are the dimensions of today's block paths, in their rank.
var Aliases = []Alias{
	{DimOrganization, "pr.owner"},
	{DimRepo, "pr.repo_name"},
	{DimUser, "pr.author"},
	{DimTeam, "pr.requested_teams"},
	{DimAssociation, "pr.association"},
	{DimLabel, "pr.labels"},
	{DimBase, "pr.base"},
	{DimStage, "turn.stage"},
	{DimReviewer, "pr.address.reviewers"},
	{DimKind, "brief.kind"},
	{DimSize, "brief.size"},
	{DimRisk, "brief.risk"},
}

// Dims lists the aliased dimensions in their canonical order.
var Dims = func() []Dim {
	out := make([]Dim, len(Aliases))
	for i, a := range Aliases {
		out[i] = a.Dim
	}
	return out
}()

// normalizers fold a fact's value the way a block path writes it, after
// the case folding every dimension gets.
var normalizers = map[string]func(string) string{
	"pr.association":       func(s string) string { return strings.ReplaceAll(s, "_", "-") },
	"pr.address.reviewers": bareLogin,
}

// DimDef is a block path condition: an alias, a fact flagged dim, or an
// enum field of a brief intent's extra.
type DimDef struct {
	Name Dim
	Fact string
	// Providers are those whose intents a block may name the dim under;
	// nil for every provider.
	Providers []unit.ProviderID
	Closed    map[unit.ProviderID][]string
	// List says the fact is a list, which a condition matches by one element.
	List bool
	// Brief says the value comes from the brief: a turn without one skips the block.
	Brief bool
	// Gated says only an intent whose manifest's dims list it may name it.
	Gated bool
	// Extra is the name of the brief intent's extra field the dim reads; ""
	// for a fact of the table.
	Extra string
}

func (d DimDef) normalize(v string) string {
	v = fold(v)
	if n := normalizers[d.Fact]; n != nil {
		return n(v)
	}
	return v
}

// closedFor are the values d takes under intents serving providers; ok is
// false when one of them takes any value.
func (d DimDef) closedFor(providers []unit.ProviderID) (vals []string, ok bool) {
	if vs, any := d.Closed[""]; any {
		return vs, true
	}
	if len(providers) == 0 || len(d.Closed) == 0 {
		return nil, false
	}
	for _, p := range providers {
		vs, closed := d.Closed[p]
		if !closed {
			return nil, false
		}
		for _, v := range vs {
			if !slices.Contains(vals, v) {
				vals = append(vals, v)
			}
		}
	}
	return vals, true
}

func factDef(f builtin.Fact, name Dim) DimDef {
	d := DimDef{Name: name, Fact: f.Name, List: f.Type == expr.List, Brief: f.Slot == builtin.BriefSlot,
		Gated: slices.Contains(builtin.IntentDims, string(name))}
	if f.Provider != "" {
		d.Providers = []unit.ProviderID{f.Provider}
	}
	if f.Enum != nil {
		d.Closed = map[unit.ProviderID][]string{}
		for p, vs := range f.Enum {
			for _, v := range vs {
				d.Closed[p] = append(d.Closed[p], d.normalize(v))
			}
		}
	}
	return d
}

// Vocabulary is every dimension a block path may name for the intents of
// the registry: the aliases in their rank, then the facts flagged dim and
// the brief intents' enum extras, by name.
func Vocabulary(intents *dintent.Registry) []DimDef {
	facts := map[string]builtin.Fact{}
	for _, f := range builtin.Facts {
		if f.Dim {
			facts[f.Name] = f
		}
	}
	var out []DimDef
	bound := map[string]bool{}
	for _, a := range Aliases {
		d := factDef(facts[a.Fact], a.Dim)
		d.Fact = a.Fact
		out = append(out, d)
		bound[a.Fact] = true
	}
	taken := func(n Dim) bool { return slices.ContainsFunc(out, func(d DimDef) bool { return d.Name == n }) }
	var more []DimDef
	for name, f := range facts {
		if !bound[name] {
			more = append(more, factDef(f, Dim(name)))
		}
	}
	for _, e := range extras(intents) {
		if !taken(e.Name) && !isTagKey(string(e.Name)) {
			more = append(more, e)
		}
	}
	slices.SortFunc(more, func(a, b DimDef) int { return cmp.Compare(a.Name, b.Name) })
	return append(out, more...)
}

// extras are the enum fields of the brief intents' extra, one per name
// across providers.
func extras(intents *dintent.Registry) []DimDef {
	if intents == nil {
		return nil
	}
	var out []DimDef
	for _, d := range intents.All() {
		if d.Builds != builtin.BuildsBrief {
			continue
		}
		providers := d.Providers
		if providers == nil {
			providers = []unit.ProviderID{d.ProviderOrDefault()}
		}
		for _, p := range d.Extra {
			if len(p.Enum) == 0 || p.Type != "string" && p.Type != "array" {
				continue
			}
			i := slices.IndexFunc(out, func(e DimDef) bool { return e.Name == Dim(p.Name) })
			if i < 0 {
				out = append(out, DimDef{Name: Dim(p.Name), Fact: "brief.extra." + p.Name, Extra: p.Name, List: p.Type == "array", Brief: true,
					Closed: map[unit.ProviderID][]string{}})
				i = len(out) - 1
			}
			for _, pv := range providers {
				if !slices.Contains(out[i].Providers, pv) {
					out[i].Providers = append(out[i].Providers, pv)
				}
				out[i].Closed[pv] = p.Enum
			}
		}
	}
	return out
}

func lookup(vocab []DimDef, name Dim) (DimDef, bool) {
	i := slices.IndexFunc(vocab, func(d DimDef) bool { return d.Name == name })
	if i < 0 {
		return DimDef{}, false
	}
	return vocab[i], true
}

// defsOf are the intents whose manifest names kind as their profile.
func defsOf(intents *dintent.Registry, kind Kind) []dintent.Def {
	if intents == nil {
		return nil
	}
	var out []dintent.Def
	for _, d := range intents.All() {
		if d.Profile == string(kind) {
			out = append(out, d)
		}
	}
	return out
}

// providersOf are the providers the intents of kind serve; nil when one of
// them serves any.
func providersOf(intents *dintent.Registry, kind Kind) []unit.ProviderID {
	var out []unit.ProviderID
	for _, d := range defsOf(intents, kind) {
		ps := d.Providers
		if ps == nil {
			ps = []unit.ProviderID{d.ProviderOrDefault()}
		}
		for _, p := range ps {
			if p == dintent.AnyProvider {
				return nil
			}
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// appliesUnder says whether a block of kind may name d.
func (d DimDef) appliesUnder(intents *dintent.Registry, kind Kind) bool {
	if d.Providers == nil {
		return true
	}
	ps := providersOf(intents, kind)
	if ps == nil {
		return true
	}
	return slices.ContainsFunc(ps, func(p unit.ProviderID) bool { return slices.Contains(d.Providers, p) })
}

// rankOf orders d among a path's conditions: an alias by its rank, any
// other dimension after them, by name.
func rankOf(d Dim) int {
	if i := slices.Index(Dims, d); i >= 0 {
		return i
	}
	return len(Aliases)
}
