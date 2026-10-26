package profile

import (
	"slices"
	"strings"
)

const (
	Applies    = "applies"
	NeedsBrief = "needs the brief"
	Trimmed    = "dropped: over the judgment limit"
	Overridden = "replaced by an override"
	Depends    = "depends on the pull request"
	Elsewhere  = "not in this repository"
)

// Listed is a block of the tree with its conditions in words and what it
// does for a pull request with given facts.
type Listed struct {
	Block      Block
	Conditions string
	State      string
}

// Conditions says in words when a block of p applies, with the paths its
// front matter asks for.
func Conditions(p BlockPath, paths []string) string {
	var out []string
	for _, c := range p.Conds {
		out = append(out, string(c.Dim)+" "+c.Value)
	}
	for _, t := range p.Tags {
		out = append(out, "tag "+strings.Replace(t, ":", "=", 1))
	}
	if len(paths) > 0 {
		out = append(out, "paths "+strings.Join(paths, " "))
	}
	if len(out) == 0 {
		return "every pull request"
	}
	return strings.Join(out, ", ")
}

func (f Facts) inRepo(p BlockPath, vocab []DimDef) bool {
	for _, c := range p.Conds {
		if c.Dim == DimOrganization || c.Dim == DimRepo {
			d, _ := lookup(vocab, c.Dim)
			if !slices.ContainsFunc(f.values(d.Fact), func(v string) bool { return d.normalize(v) == c.Value }) {
				return false
			}
		}
	}
	return true
}

// List gives every block of kind, in the tree's order, with what it does
// for a pull request with facts f.
func (t Tree) List(kind Kind, f Facts) []Listed {
	g := t.Compose(kind, f)
	in := func(list []string, b Block) bool { return slices.Contains(list, source(b)) }
	var out []Listed
	for _, b := range t.Blocks {
		if b.Path.Intent != kind {
			continue
		}
		l := Listed{Block: b, Conditions: Conditions(b.Path, b.FM.Paths)}
		ok, _ := f.match(b, t.vocab())
		switch {
		case slices.ContainsFunc(g.Parts, func(p Part) bool { return p.File == b.File }):
			l.State = Applies
		case in(g.Skipped, b):
			l.State = NeedsBrief
		case in(g.Dropped, b):
			l.State = Trimmed
		case ok:
			l.State = Overridden
		case f.inRepo(b.Path, t.vocab()):
			l.State = Depends
		default:
			l.State = Elsewhere
		}
		out = append(out, l)
	}
	return out
}
