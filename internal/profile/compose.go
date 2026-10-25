package profile

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/brand"
)

// Facts are what a block's conditions are matched against. Kind, Size,
// Risk, Tags (key:value) and Files come from the brief and count only when
// HasBrief is true.
type Facts struct {
	Owner, Repo, Author, Association, Base string
	// Teams are the teams a review was requested through, as <org>-<slug>.
	Teams, Labels []string
	// Round is first or follow-up.
	Round string
	// Reviewers are the reviewers an address round answers.
	Reviewers        []string
	Kind, Size, Risk string
	Tags, Files      []string
	HasBrief         bool
}

type seg struct {
	rank       int
	key, value string
}

func segsOf(p BlockPath) []seg {
	out := make([]seg, 0, len(p.Conds)+len(p.Tags))
	for _, c := range p.Conds {
		out = append(out, seg{rank: rank(c.Dim), key: string(c.Dim), value: c.Value})
	}
	for _, t := range p.Tags {
		k, v, _ := strings.Cut(t, ":")
		out = append(out, seg{rank: len(Dims), key: k, value: v})
	}
	return out
}

func compareSegs(a, b []seg) int {
	for i := range min(len(a), len(b)) {
		if c := cmp.Or(cmp.Compare(a[i].rank, b[i].rank), cmp.Compare(a[i].key, b[i].key), cmp.Compare(a[i].value, b[i].value)); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

func ancestorOf(a, b []seg) bool { return len(a) <= len(b) && slices.Equal(a, b[:len(a)]) }

func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func holds(list []string, v string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return fold(s) == v })
}

// bareLogin drops the "[bot]" GitHub's REST API gives a bot's login.
func bareLogin(login string) string { return strings.TrimSuffix(login, "[bot]") }

func (f Facts) value(d Dim) (string, bool) {
	switch d {
	case DimOrganization:
		return fold(f.Owner), false
	case DimRepo:
		return fold(f.Repo), false
	case DimUser:
		return fold(f.Author), false
	case DimAssociation:
		return strings.ReplaceAll(fold(f.Association), "_", "-"), false
	case DimBase:
		return fold(f.Base), false
	case DimRound:
		return fold(f.Round), false
	case DimKind:
		return fold(f.Kind), true
	case DimSize:
		return fold(f.Size), true
	case DimRisk:
		return fold(f.Risk), true
	}
	return "", false
}

func (f Facts) match(b Block) (ok, needsBrief bool) {
	for _, c := range b.Path.Conds {
		var hit, brief bool
		switch c.Dim {
		case DimTeam:
			hit = holds(f.Teams, c.Value)
		case DimLabel:
			hit = holds(f.Labels, c.Value)
		case DimReviewer:
			hit = slices.ContainsFunc(f.Reviewers, func(r string) bool { return bareLogin(fold(r)) == bareLogin(c.Value) })
		default:
			var v string
			v, brief = f.value(c.Dim)
			hit = v == c.Value
		}
		if brief && !f.HasBrief {
			needsBrief = true
		} else if !hit {
			return false, false
		}
	}
	if len(b.Path.Tags) > 0 || len(b.FM.Paths) > 0 {
		switch {
		case !f.HasBrief:
			needsBrief = true
		case !allIn(b.Path.Tags, f.Tags), !anyFileMatches(b.FM.Paths, f.Files):
			return false, false
		}
	}
	if needsBrief {
		return false, true
	}
	return true, false
}

func allIn(want, have []string) bool {
	for _, w := range want {
		if !holds(have, w) {
			return false
		}
	}
	return true
}

func anyFileMatches(globs, files []string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, f := range files {
		for _, g := range globs {
			if matchGlob(strings.Split(g, "/"), strings.Split(strings.TrimPrefix(f, "/"), "/")) {
				return true
			}
		}
	}
	return false
}

func matchGlob(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchGlob(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], name[0])
	return ok && matchGlob(pattern[1:], name[1:])
}

func segLabel(s seg) string {
	switch Dim(s.key) {
	case DimOrganization:
		return "Organization " + s.value
	case DimRepo:
		return "Repository " + s.value
	case DimUser:
		return "User " + s.value
	case DimTeam:
		return "Team " + s.value
	case DimAssociation:
		return "Author association " + s.value
	case DimLabel:
		return "Label " + s.value
	case DimBase:
		return "Base branch " + s.value
	case DimRound:
		if s.value == "first" {
			return "First rounds"
		}
		return "Follow-up rounds"
	case DimReviewer:
		return "Reviewer " + s.value
	case DimKind:
		return "Kind " + s.value
	case DimSize:
		return "Size " + s.value
	case DimRisk:
		return "Risk " + s.value
	}
	return "Tag " + s.key + "=" + s.value
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

func source(b Block) string { return b.Layer.Name + ": " + b.File }

type placed struct {
	b    Block
	segs []seg
}

func heading(kind Kind, before []placed, p placed) (int, string) {
	bundled := p.b.Layer.Dir == ""
	var label string
	level := 3
	if len(p.segs) == 0 {
		label = "Your " + string(kind) + " guide"
		if bundled {
			label = brand.Name + "'s " + string(kind) + " guide"
		}
	} else {
		in := map[int]bool{}
		for _, q := range before {
			if n := len(q.segs); n > 0 && n < len(p.segs) && ancestorOf(q.segs, p.segs) {
				in[n] = true
			}
		}
		deepest := 0
		for n := range in {
			deepest = max(deepest, n)
		}
		level = min(3+len(in), maxLevel)
		parts := make([]string, 0, len(p.segs)-deepest)
		for i, s := range p.segs[deepest:] {
			l := segLabel(s)
			if i > 0 {
				l = lowerFirst(l)
			}
			parts = append(parts, l)
		}
		label = strings.Join(parts, ", ")
	}
	if p.b.FM.Title != "" {
		label = p.b.FM.Title
	}
	if bundled && len(p.segs) > 0 {
		label += " (" + brand.Name + ")"
	}
	if p.b.FM.Override {
		label += ", replaces the guidance above it"
	}
	return level, label
}

func (b Block) body(level int) string {
	var sb strings.Builder
	sb.WriteString(shiftHeadings(strings.TrimSpace(b.Text), min(level+1, maxLevel)))
	for _, inc := range b.Includes {
		sb.WriteString("\n\n" + strings.Repeat("#", min(level+1, maxLevel)) + " " + inc.Name + "\n\n")
		sb.WriteString(shiftHeadings(strings.TrimSpace(inc.Text), min(level+2, maxLevel)))
	}
	return strings.TrimSpace(sb.String())
}

// Compose matches the blocks of kind against f and puts them in the text's
// order: depth first through the tree of paths, a node's own blocks (lower
// layer first) before its children by dimension rank and value, tags last.
// An override drops the blocks before it whose path is an ancestor of its
// own. Over JudgmentMax, blocks other than the root ones are dropped from
// the end.
func (t Tree) Compose(kind Kind, f Facts) Guide {
	g := Guide{Kind: kind}
	var items []placed
	for _, b := range t.Blocks {
		if b.Path.Intent != kind {
			continue
		}
		ok, needs := f.match(b)
		if needs {
			g.Skipped = append(g.Skipped, source(b))
		}
		if ok {
			items = append(items, placed{b: b, segs: segsOf(b.Path)})
		}
	}
	isUser := func(p placed) bool { return p.b.Layer.Dir != "" }
	slices.SortStableFunc(items, func(a, b placed) int {
		return cmp.Or(compareSegs(a.segs, b.segs), compareBool(isUser(a), isUser(b)))
	})
	var kept []placed
	for _, it := range items {
		if it.b.FM.Override {
			kept = slices.DeleteFunc(kept, func(k placed) bool { return ancestorOf(k.segs, it.segs) })
		}
		kept = append(kept, it)
	}
	size := 0
	for i, it := range kept {
		level, label := heading(kind, kept[:i], it)
		p := Part{Layer: it.b.Layer, File: it.b.File, Path: it.b.Rel(), Label: label, Level: level,
			Override: it.b.FM.Override, Text: it.b.body(level)}
		size += len(p.Label) + len(p.Text)
		g.Parts = append(g.Parts, p)
	}
	for i := len(kept) - 1; i >= 0 && size > JudgmentMax; i-- {
		if len(kept[i].segs) == 0 {
			continue
		}
		size -= len(g.Parts[i].Label) + len(g.Parts[i].Text)
		g.Dropped = append(g.Dropped, source(kept[i].b))
		g.Parts = slices.Delete(g.Parts, i, i+1)
		kept = slices.Delete(kept, i, i+1)
	}
	for _, it := range kept {
		if it.b.FM.Delegates != nil {
			g.Delegates, g.DelegatesFrom = it.b.FM.Delegates, it.b.File
		}
	}
	return g
}
