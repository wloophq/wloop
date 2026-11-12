package outboxtest

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Kinds are draft kinds in memory. Validate takes a payload that is a JSON
// object, or empty for a kind with no Required fields, whose top-level fields
// are all in Fields of its kind when the kind is there, and that has every
// Required field of its kind.
type Kinds struct {
	Specs []draft.KindSpec
	// Fields are each kind's payload fields, by "<provider>/<name>"; a kind absent takes any field.
	Fields map[string][]string
	// Defaults are each provider's default delivery, which Shape gives a draft of no kind and an
	// undeclared delivery.
	Defaults map[string]string
	// Required are each kind's required payload fields, by "<provider>/<name>".
	Required map[string][]string
}

var _ outbox.Kinds = Kinds{}

func (k Kinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	for _, s := range k.Specs {
		if s.Provider == provider && s.Name == kind {
			return s, true
		}
	}
	return draft.KindSpec{}, false
}

// Shape mirrors draft.Catalog's Shape over Specs, with Defaults as each provider's default delivery.
func (k Kinds) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	mine := slices.DeleteFunc(slices.Clone(k.Specs), func(s draft.KindSpec) bool { return s.Provider != provider })
	if len(mine) == 0 {
		d.Purpose = cmp.Or(d.Purpose, string(d.Kind))
		return d, nil
	}
	name := string(d.Kind)
	if name == "" {
		name = k.kindOf(mine, d.Purpose, string(d.Delivery), k.Defaults[provider])
	}
	spec, ok := k.Lookup(provider, name)
	if !ok {
		return d, fmt.Errorf("%w: %w: %q of %s", outbox.ErrInvalid, draft.ErrUnknownKind, cmp.Or(name, d.Purpose), provider)
	}
	d.Kind, d.Purpose, d.Delivery = draft.Kind(spec.Name), cmp.Or(d.Purpose, spec.Name), draft.Delivery(spec.Delivery)
	return d, nil
}

func (Kinds) kindOf(mine []draft.KindSpec, purpose, delivery, fallback string) string {
	declared := slices.ContainsFunc(mine, func(s draft.KindSpec) bool { return s.Delivery == delivery })
	if i := slices.IndexFunc(mine, func(s draft.KindSpec) bool { return s.Name == purpose }); i >= 0 &&
		(delivery == "" || !declared || mine[i].Delivery == delivery) {
		return purpose
	}
	if !declared {
		delivery = fallback
	}
	if i := slices.IndexFunc(mine, func(s draft.KindSpec) bool { return s.Delivery == delivery }); i >= 0 {
		return mine[i].Name
	}
	return ""
}

func (k Kinds) Validate(spec draft.KindSpec, payload json.RawMessage) error {
	key := spec.Provider + "/" + spec.Name
	var fields map[string]json.RawMessage
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
			return fmt.Errorf("%w: the payload of %s is not a JSON object", outbox.ErrInvalid, key)
		}
	}
	for _, f := range k.Required[key] {
		if _, ok := fields[f]; !ok {
			return fmt.Errorf("%w: %s needs payload field %q", outbox.ErrInvalid, key, f)
		}
	}
	allowed, ok := k.Fields[key]
	if !ok {
		return nil
	}
	for f := range fields {
		if !slices.Contains(allowed, f) {
			return fmt.Errorf("%w: %s/%s takes no payload field %q", outbox.ErrInvalid, spec.Provider, spec.Name, f)
		}
	}
	return nil
}

// Bases are each unit's live basis in memory, as Set left it; a unit never set
// is "". Live counts its calls, so a test sees the outbox read the live value.
type Bases struct {
	mu    sync.Mutex
	live  map[unit.Unit]string
	calls int
	// Err, when set, fails every Live.
	Err error
}

var _ outbox.Bases = (*Bases)(nil)

// Set makes u's live basis b.
func (b *Bases) Set(u unit.Unit, basis string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.live == nil {
		b.live = map[unit.Unit]string{}
	}
	b.live[u] = basis
}

func (b *Bases) Live(_ context.Context, u unit.Unit, _ string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.Err != nil {
		return "", b.Err
	}
	return b.live[u], nil
}

// Calls are how many times Live was called.
func (b *Bases) Calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}
