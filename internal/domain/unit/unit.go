// Package unit names a unit of work by its provider and the provider's key.
package unit

import (
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

type Registry struct{ codecs map[ProviderID]Codec }

func NewRegistry(cs ...Codec) *Registry {
	r := &Registry{codecs: make(map[ProviderID]Codec, len(cs))}
	for _, c := range cs {
		r.codecs[c.Provider()] = c
	}
	return r
}

func (r *Registry) Parse(provider, key string) (Unit, error) {
	c, ok := r.codecs[ProviderID(provider)]
	if !ok {
		return Unit{}, fmt.Errorf("unknown provider %s", provider)
	}
	if err := c.Valid(key); err != nil {
		return Unit{}, err
	}
	return Unit{Provider: ProviderID(provider), Key: key}, nil
}

func (r *Registry) Providers() []ProviderID {
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
