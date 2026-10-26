package profile

import (
	"errors"
	"io/fs"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/frontmatter"
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
// keys replacing a lower one's.
func LoadLabels(chain []Layer) (Labels, error) {
	var out Labels
	for _, l := range chain {
		r, err := readLayer(l, LabelsFile)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Labels{}, err
		}
		var own Labels
		if err := frontmatter.Decode(fileName(l, LabelsFile), r.Body, 1, &own); err != nil {
			return Labels{}, err
		}
		out = out.over(own)
	}
	return out, nil
}

var bundledLabels = sync.OnceValue(func() Labels {
	l, _ := LoadLabels([]Layer{{Name: "bundled", Source: "embedded"}})
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
