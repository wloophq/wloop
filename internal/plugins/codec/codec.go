// Package codec is a plugin manifest's codec as the host's unit registry
// reads it.
package codec

import (
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// codec is a manifest's codec as the host's unit registry reads it, which
// the host uses for a plugin's units whether or not the plugin runs.
type codec struct {
	id       unit.ProviderID
	c        *manifest.Compiled
	labelled bool
	target   string
}

// linkedCodec is a codec whose units have web addresses.
type linkedCodec struct{ codec }

// Of is plugin id's unit codec from its manifest's codec table. It is a
// unit.Linker only when the table declares URLs.
func Of(id string, m manifest.Codec) (unit.Codec, error) {
	c, err := m.Compile()
	if err != nil {
		return nil, err
	}
	out := codec{id: unit.ProviderID(id), c: c, labelled: m.Label != "", target: m.Target}
	if len(m.URLs) > 0 {
		return linkedCodec{out}, nil
	}
	return out, nil
}

func (c codec) Provider() unit.ProviderID { return c.id }

func (c codec) Valid(key string) error { return c.c.Valid(key) }

func (c codec) Equal(a, b string) bool { return c.c.Equal(a, b) }

// Label is the manifest's label of key; provider:key when it declares none.
func (c codec) Label(key string) string {
	if !c.labelled {
		return unit.Unit{Provider: c.id, Key: key}.String()
	}
	return c.c.Label(key)
}

// Target is how a command line names a unit; <id:key> when the manifest
// says nothing.
func (c codec) Target() string {
	if c.target == "" {
		return "<" + string(c.id) + ":key>"
	}
	return c.target
}

func (c linkedCodec) URL(key string) string { return c.c.URL(key) }

func (c linkedCodec) KeyOf(url string) (string, bool) { return c.c.KeyOf(url) }

var (
	_ unit.Equaler  = codec{}
	_ unit.Labeler  = codec{}
	_ unit.Targeter = codec{}
	_ unit.Linker   = linkedCodec{}
)
