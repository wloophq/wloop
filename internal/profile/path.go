package profile

import (
	"cmp"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/facet"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/tag"
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
	DimRound        Dim = dintent.DimRound
	DimReviewer     Dim = dintent.DimReviewer
	DimKind         Dim = "kind"
	DimSize         Dim = "size"
	DimRisk         Dim = "risk"
)

// Dims lists the dimensions in their canonical order.
var Dims = []Dim{DimOrganization, DimRepo, DimUser, DimTeam, DimAssociation, DimLabel, DimBase, DimRound, DimReviewer, DimKind, DimSize, DimRisk}

var dimValues = map[Dim][]string{
	DimAssociation: {"owner", "member", "collaborator", "contributor", "first-timer", "first-time-contributor", "mannequin", "none"},
	DimRound:       {"first", "follow-up"},
	DimKind:        facet.Kinds,
	DimSize:        facet.Sizes,
	DimRisk:        facet.Risks,
}

// Kinds lists the intents a block can be written for: every profile an
// intent of the registry names, sorted.
func Kinds(intents *dintent.Registry) []Kind {
	var out []Kind
	if intents == nil {
		return nil
	}
	for _, d := range intents.All() {
		if d.Profile != "" && !slices.Contains(out, Kind(d.Profile)) {
			out = append(out, Kind(d.Profile))
		}
	}
	slices.Sort(out)
	return out
}

// DimsOf are the dimension conditions valid under kind: those its intent's manifest lists.
func DimsOf(intents *dintent.Registry, kind Kind) []string {
	if intents == nil {
		return nil
	}
	d, _ := intents.Get(dintent.Name(kind))
	return d.Dims
}

func kindNames(intents *dintent.Registry) string {
	ks := Kinds(intents)
	names := make([]string, len(ks))
	for i, k := range ks {
		names[i] = string(k)
	}
	switch len(names) {
	case 0:
		return "none"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

type Cond struct {
	Dim   Dim
	Value string
}

// BlockPath is where a block sits in a layer: its conditions in canonical
// order, its tags as key:value sorted, and its intent.
type BlockPath struct {
	Conds  []Cond
	Tags   []string
	Intent Kind
}

// Root says the block has no condition, so it applies to every pull request.
func (p BlockPath) Root() bool { return len(p.Conds) == 0 && len(p.Tags) == 0 }

type PathError struct{ File, Problem, Fix string }

func (e PathError) Error() string {
	s := e.File + ": " + e.Problem
	if e.Fix != "" {
		s += "; " + e.Fix
	}
	return s
}

func rank(d Dim) int { return slices.Index(Dims, d) }

func dimNames() string {
	names := make([]string, len(Dims))
	for i, d := range Dims {
		names[i] = string(d)
	}
	return strings.Join(names, ", ")
}

func tagKeyNames() string {
	names := make([]string, len(tag.Keys))
	for i, k := range tag.Keys {
		names[i] = k.Name
	}
	return strings.Join(names, ", ")
}

func tagFix(k string) string {
	for _, t := range tag.Keys {
		if t.Name == k && !t.Open() {
			return k + " is one of " + strings.Join(t.Closed, ", ")
		}
	}
	return "use lower-case letters, digits and -, starting with a letter, at most 32 bytes"
}

func validKind(intents *dintent.Registry, s string) bool {
	return slices.Contains(Kinds(intents), Kind(s))
}

func escape(v string) string {
	return strings.NewReplacer("%", "%25", "/", "%2F").Replace(v)
}

func inGrammar(dirs []string, leaf string) bool {
	if strings.HasPrefix(leaf, "intent=") || len(dirs) > 0 && dirs[0] == "tags" {
		return true
	}
	return slices.ContainsFunc(dirs, func(s string) bool { return strings.Contains(s, "=") })
}

// ParsePath reads rel, a slash-separated path relative to a layer's
// directory, against the intents of the registry. ok is false for a file
// that is not a block; err names a file that claims to be one and breaks the grammar.
func ParsePath(rel string, intents *dintent.Registry) (p BlockPath, ok bool, err error) {
	segs := strings.Split(rel, "/")
	leaf, dirs := segs[len(segs)-1], segs[:len(segs)-1]
	if !strings.HasSuffix(leaf, ".md") {
		return BlockPath{}, false, nil
	}
	bad := func(problem, fix string) (BlockPath, bool, error) {
		return BlockPath{}, false, PathError{File: rel, Problem: problem, Fix: fix}
	}
	name := strings.TrimSuffix(leaf, ".md")
	if !inGrammar(dirs, leaf) {
		return BlockPath{}, false, nil
	}
	inTags := false
	var respelled, afterTags string
	for _, s := range dirs {
		if s == "tags" {
			if inTags {
				return bad("tags appears twice", "keep one tags/ directory, after the conditions")
			}
			inTags = true
			continue
		}
		k, raw, found := strings.Cut(s, "=")
		if !found {
			return bad("directory "+s+" is not a dim=value condition", "move the file out of the condition directories, or name the directory <dim>=<value>")
		}
		v, uerr := url.PathUnescape(raw)
		if uerr != nil || v == "" {
			return bad("directory "+s+" has no valid value", "write the value after =, escaping / as %2F and % as %25")
		}
		v = strings.ToLower(v)
		d := Dim(k)
		if inTags && rank(d) < 0 {
			if !slices.ContainsFunc(tag.Keys, func(t tag.Key) bool { return t.Name == k }) {
				return bad("unknown tag key "+k, "the tag keys are "+tagKeyNames())
			}
			n, valid := tag.Normalize(k, v)
			if !valid {
				return bad("tag "+k+"="+v+" is not a valid value", tagFix(k))
			}
			if n != v && respelled == "" {
				respelled = "tag " + k + "=" + v + " is spelled " + k + "=" + n
			}
			p.Tags = append(p.Tags, k+":"+n)
			continue
		}
		if inTags && afterTags == "" {
			afterTags = "condition " + k + " comes after tags/; conditions come before it"
		}
		if rank(d) < 0 {
			return bad("unknown dimension "+k, "the dimensions are "+dimNames())
		}
		if vals, closed := dimValues[d]; closed && !slices.Contains(vals, v) {
			return bad(k+"="+v+" is not a valid value", k+" is one of "+strings.Join(vals, ", "))
		}
		p.Conds = append(p.Conds, Cond{Dim: d, Value: v})
	}
	if inTags && len(p.Tags) == 0 && afterTags == "" {
		return bad("tags/ names no tag", "add a <key>=<value> directory under tags/")
	}
	switch intent, isLeaf := strings.CutPrefix(name, "intent="); {
	case isLeaf && validKind(intents, intent):
		p.Intent = Kind(intent)
	case isLeaf:
		return bad("unknown intent "+intent, "the intents with judgment blocks are "+kindNames(intents))
	case validKind(intents, strings.ToLower(name)):
		p.Intent = Kind(strings.ToLower(name))
		if c, err := canonical(p, intents); err == nil {
			return bad(leaf+" is not a block name", "rename it to "+c)
		}
		return bad(leaf+" is not a block name", "rename it to intent="+leaf)
	default:
		return bad(leaf+" inside a condition directory is not a block", "name it intent=<intent>.md, or move it out of the condition directories")
	}
	for _, problem := range []string{afterTags, respelled} {
		if problem == "" {
			continue
		}
		if c, err := canonical(p, intents); err == nil {
			return bad(problem, "rename it to "+c)
		}
		return bad(problem, "")
	}
	if err := checkOrder(p, intents); err != nil {
		var e PathError
		if !errors.As(err, &e) {
			return BlockPath{}, false, err
		}
		e.File = rel
		return BlockPath{}, false, e
	}
	return p, true, nil
}

func sorted(p BlockPath) BlockPath {
	out := BlockPath{Conds: slices.Clone(p.Conds), Tags: slices.Clone(p.Tags), Intent: p.Intent}
	slices.SortStableFunc(out.Conds, func(a, b Cond) int { return cmp.Compare(rank(a.Dim), rank(b.Dim)) })
	slices.Sort(out.Tags)
	return out
}

func write(p BlockPath) string {
	var b strings.Builder
	for _, c := range p.Conds {
		b.WriteString(string(c.Dim) + "=" + escape(c.Value) + "/")
	}
	if len(p.Tags) > 0 {
		b.WriteString("tags/")
		for _, t := range p.Tags {
			k, v, _ := strings.Cut(t, ":")
			b.WriteString(k + "=" + escape(v) + "/")
		}
	}
	b.WriteString("intent=" + string(p.Intent) + ".md")
	return b.String()
}

func canonical(p BlockPath, intents *dintent.Registry) (string, error) {
	s := sorted(p)
	if err := checkOrder(s, intents); err != nil {
		return "", err
	}
	return write(s), nil
}

func checkOrder(p BlockPath, intents *dintent.Registry) error {
	s := sorted(p)
	for i := 1; i < len(s.Conds); i++ {
		if s.Conds[i].Dim == s.Conds[i-1].Dim {
			return PathError{Problem: string(s.Conds[i].Dim) + " appears twice", Fix: "a block takes one value per dimension"}
		}
	}
	for i := 1; i < len(s.Tags); i++ {
		if s.Tags[i] == s.Tags[i-1] {
			return PathError{Problem: "tag " + s.Tags[i] + " appears twice", Fix: "drop the repeated tag"}
		}
	}
	has := func(d Dim) bool { return slices.ContainsFunc(s.Conds, func(c Cond) bool { return c.Dim == d }) }
	if has(DimRepo) && !has(DimOrganization) {
		return PathError{Problem: "repo is only valid right after organization", Fix: "put it under organization=<owner>/"}
	}
	for _, d := range []Dim{DimRound, DimReviewer} {
		if has(d) && !slices.Contains(DimsOf(intents, p.Intent), string(d)) {
			return PathError{Problem: string(d) + " does not apply to " + string(p.Intent) + ": its manifest's dims do not list it",
				Fix: "drop the " + string(d) + " condition, or move the block to an intent whose dims list " + string(d)}
		}
	}
	if !slices.Equal(s.Conds, p.Conds) || !slices.Equal(s.Tags, p.Tags) {
		return PathError{Problem: "conditions are out of order", Fix: "rename it to " + write(s)}
	}
	return nil
}

// PathOf writes p's canonical path, the one ParsePath reads back as p.
func PathOf(p BlockPath, intents *dintent.Registry) (string, error) {
	low := BlockPath{Intent: p.Intent}
	for _, c := range p.Conds {
		low.Conds = append(low.Conds, Cond{Dim: c.Dim, Value: strings.ToLower(c.Value)})
	}
	low.Tags = p.Tags
	rel := write(sorted(low))
	if _, ok, err := ParsePath(rel, intents); err != nil {
		return "", err
	} else if !ok {
		return "", PathError{File: rel, Problem: "not a block path"}
	}
	return rel, nil
}
