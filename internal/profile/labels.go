package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/frontmatter"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// LabelsFile is the profile file that names the headings of a block's
// conditions.
const LabelsFile = "labels.toml"

// Labels are the headings of a block's conditions, as labels.toml writes them.
type Labels struct {
	Other  string            `toml:"other"`
	Tag    string            `toml:"tag"`
	Dims   map[string]string `toml:"dims"`
	Values map[string]string `toml:"values"`
}

// over lays the keys o sets over l.
func (l Labels) over(o Labels) Labels {
	out := Labels{Other: l.Other, Tag: l.Tag, Dims: map[string]string{}, Values: map[string]string{}}
	for k, v := range l.Dims {
		out.Dims[k] = v
	}
	for k, v := range l.Values {
		out.Values[k] = v
	}
	if o.Other != "" {
		out.Other = o.Other
	}
	if o.Tag != "" {
		out.Tag = o.Tag
	}
	for k, v := range o.Dims {
		out.Dims[k] = v
	}
	for k, v := range o.Values {
		out.Values[k] = v
	}
	return out
}

// LoadLabels reads labels.toml from each layer of chain, a higher layer's
// keys replacing a lower one's; a heading names a dimension of vocab.
func LoadLabels(chain []Layer, vocab []DimDef) (Labels, error) {
	var out Labels
	for _, l := range chain {
		r, err := ReadFile(l, LabelsFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Labels{}, err
		}
		file := fileName(l, LabelsFile)
		if err := checkLabels(file, r.Body, vocab); err != nil {
			return Labels{}, err
		}
		var own Labels
		if err := frontmatter.Decode(file, r.Body, 1, &own); err != nil {
			return Labels{}, err
		}
		out = out.over(own)
	}
	return out, nil
}

// checkLabels is the first problem of src: the grammar's, then a heading
// of a dimension vocab does not hold.
func checkLabels(file string, src []byte, vocab []DimDef) error {
	if err := checkGrammar(builtin.SurfaceLabels, file, src, 1); err != nil {
		return err
	}
	var l Labels
	if toml.Unmarshal(src, &l) != nil {
		return nil
	}
	names := make([]string, len(vocab))
	for i, d := range vocab {
		names[i] = string(d.Name)
	}
	var ps []builtin.Problem
	for _, k := range slices.Sorted(maps.Keys(l.Dims)) {
		if !slices.Contains(names, k) {
			ps = append(ps, builtin.Problem{Path: "dims." + k, Msg: fmt.Sprintf("unknown dimension %q%s", k, expr.DidYouMean(k, names))})
		}
	}
	for _, k := range slices.Sorted(maps.Keys(l.Values)) {
		dim, _, ok := strings.Cut(k, "=")
		switch {
		case !ok:
			ps = append(ps, builtin.Problem{Path: "values." + k, Msg: "must be <dim>=<value>"})
		case !slices.Contains(names, dim):
			ps = append(ps, builtin.Problem{Path: "values." + k, Msg: fmt.Sprintf("unknown dimension %q%s", dim, expr.DidYouMean(dim, names))})
		}
	}
	return grammarError(file, src, 1, ps)
}

// checkGrammar is the grammar's first problem with src, a file of surface
// whose first line is line; nil when it has none.
func checkGrammar(surface builtin.Surface, file string, src []byte, line int) error {
	var doc map[string]any
	if toml.Unmarshal(src, &doc) != nil {
		return nil
	}
	return grammarError(file, src, line, builtin.Check(surface, doc))
}

func grammarError(file string, src []byte, line int, ps []builtin.Problem) error {
	if len(ps) == 0 {
		return nil
	}
	at := frontmatter.Positions(src, line)[ps[0].Path]
	return &frontmatter.Error{File: file, Line: max(at.Line, line), Col: max(at.Col, 1), Key: ps[0].Path, Msg: ps[0].Msg}
}

var bundledLabels = sync.OnceValue(func() Labels {
	l, _ := LoadLabels([]Layer{{Name: "bundled", Source: "embedded"}}, Vocabulary(nil))
	return l
})

func (l Labels) of(s seg) string {
	text, ok := l.Values[s.key+"="+s.value]
	switch {
	case ok:
	case s.rank > len(Aliases):
		text = l.Tag
	default:
		text = l.Dims[s.key]
		if text == "" {
			text = l.Other
		}
	}
	if text == "" {
		text = "{Dim} {value}"
	}
	return strings.NewReplacer("{value}", s.value, "{key}", s.key, "{dim}", s.key, "{Dim}", upperFirst(s.key)).Replace(text)
}
