// Package unit names a unit of work by its provider and the provider's key.
package unit

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type ProviderID string

var providerRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidProvider says whether id has a provider id's shape: a lowercase
// letter, then up to 31 lowercase letters, digits, dashes or underscores.
func ValidProvider(id string) bool { return providerRe.MatchString(id) }

const (
	GitHub    ProviderID = "github"
	Session   ProviderID = "session"
	Workspace ProviderID = "workspace"
	// Task is the provider the core keeps for GTD entries: the key is the
	// entry's id.
	Task ProviderID = "entry"
)

type Unit struct {
	Provider ProviderID `json:"provider"`
	Key      string     `json:"key"`
}

func (u Unit) String() string { return string(u.Provider) + ":" + u.Key }

func (u Unit) IsZero() bool { return u == Unit{} }

// Label names u as users read it: a pull request by its reference alone.
func (u Unit) Label() string {
	if u.Provider == GitHub {
		return u.Key
	}
	return u.String()
}

// Less orders by provider, then key; the sort order of every unit list.
func (u Unit) Less(o Unit) bool {
	if u.Provider != o.Provider {
		return u.Provider < o.Provider
	}
	return u.Key < o.Key
}

// Codec validates the keys of one provider.
type Codec interface {
	Provider() ProviderID
	Valid(key string) error
}

// Equaler is an optional capability of a Codec: whether two keys of its
// provider name the same thing, such as logins compared without case. A codec
// without it compares keys byte for byte.
type Equaler interface {
	Equal(a, b string) bool
}

// Linker is an optional capability of a Codec: the web address of a key, and
// the key an address names. A unit of a provider with addresses is named by
// its address wherever a person or an agent types it.
type Linker interface {
	URL(key string) string
	KeyOf(url string) (key string, ok bool)
}

// KeysEqual says whether keys a and b of c's provider name the same thing.
func KeysEqual(c Codec, a, b string) bool {
	if e, ok := c.(Equaler); ok {
		return e.Equal(a, b)
	}
	return a == b
}

// KeyComparer says whether two keys of provider p name the same thing.
type KeyComparer interface {
	KeysEqual(p ProviderID, a, b string) bool
}

type Registry struct{ codecs map[ProviderID]Codec }

// KeysEqual says whether keys a and b of p name the same thing through p's
// codec; a provider r does not know compares byte for byte.
func (r *Registry) KeysEqual(p ProviderID, a, b string) bool {
	if c, ok := r.Codec(p); ok {
		return KeysEqual(c, a, b)
	}
	return a == b
}

// Same says whether a and b name the same unit.
func (r *Registry) Same(a, b Unit) bool {
	return a.Provider == b.Provider && r.KeysEqual(a.Provider, a.Key, b.Key)
}

// ParseURL is the unit a web address names, among the providers with
// addresses.
func (r *Registry) ParseURL(raw string) (Unit, bool) {
	for _, p := range r.Providers() {
		if l, ok := r.codecs[p].(Linker); ok {
			if key, ok := l.KeyOf(raw); ok {
				return Unit{Provider: p, Key: key}, true
			}
		}
	}
	return Unit{}, false
}

// URL is u's web address; ok is false for a provider without addresses.
func (r *Registry) URL(u Unit) (string, bool) {
	c, _ := r.Codec(u.Provider)
	if l, ok := c.(Linker); ok {
		return l.URL(u.Key), true
	}
	return "", false
}

// Linked says whether p's units have web addresses.
func (r *Registry) Linked(p ProviderID) bool {
	c, _ := r.Codec(p)
	_, ok := c.(Linker)
	return ok
}

// Targeter is an optional capability of a Codec: how a command line names
// one of its units, such as <url>.
type Targeter interface {
	Target() string
}

// Target is how a command line names a unit of p: its codec's Target, else
// <p:key>.
func (r *Registry) Target(p ProviderID) string {
	c, _ := r.Codec(p)
	if t, ok := c.(Targeter); ok {
		return t.Target()
	}
	return "<" + string(p) + ":key>"
}

// Targets is how a command line names a unit of any of ps, or of any
// provider of r when ps is empty: their targets as one, such as
// <url|ref|permalink|slack:key>.
func (r *Registry) Targets(ps ...ProviderID) string {
	if len(ps) == 0 && r != nil {
		for p := range r.codecs {
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
	}
	var parts []string
	seen := map[string]bool{}
	for _, p := range ps {
		for _, part := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.Target(p), "<"), ">"), "|") {
			if !seen[part] {
				seen[part] = true
				parts = append(parts, part)
			}
		}
	}
	return "<" + strings.Join(parts, "|") + ">"
}

// Codec is p's codec.
func (r *Registry) Codec(p ProviderID) (Codec, bool) {
	if r == nil {
		return nil, false
	}
	c, ok := r.codecs[p]
	return c, ok
}

func NewRegistry(cs ...Codec) *Registry {
	r := &Registry{codecs: make(map[ProviderID]Codec, len(cs))}
	for _, c := range cs {
		r.codecs[c.Provider()] = c
	}
	return r
}

func (r *Registry) Parse(provider, key string) (Unit, error) {
	c, ok := r.Codec(ProviderID(provider))
	if !ok {
		return Unit{}, fmt.Errorf("unknown provider %s", provider)
	}
	if err := c.Valid(key); err != nil {
		return Unit{}, err
	}
	return Unit{Provider: ProviderID(provider), Key: key}, nil
}

func (r *Registry) Providers() []ProviderID {
	if r == nil {
		return nil
	}
	out := make([]ProviderID, 0, len(r.codecs))
	for p := range r.codecs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// OpaqueCodec accepts any non-blank key without a line break.
type OpaqueCodec struct{ ID ProviderID }

func (c OpaqueCodec) Provider() ProviderID { return c.ID }

func (c OpaqueCodec) Valid(key string) error {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("invalid %s key %q", c.ID, key)
	}
	return nil
}

// Resolver is a codec whose provider must look an address up to find its
// unit, such as a Slack message ref or permalink to its direct message
// segment or thread; ok is false for an address
// that is not one of the provider's.
type Resolver interface {
	Resolve(ctx context.Context, address string) (key string, ok bool, err error)
}

// Locator is a codec that knows the unit a working directory holds, such as
// the pull request of a checkout's branch; ok is false when it holds none.
type Locator interface {
	UnitAt(ctx context.Context, dir string) (key string, ok bool, err error)
}
