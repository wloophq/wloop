package outboxtest

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Kinds are draft kinds in memory. Validate takes a payload that is empty or
// a JSON object, and whose top-level fields are all in Fields of its kind when
// the kind is there.
type Kinds struct {
	Specs []draft.KindSpec
	// Fields are each kind's payload fields, by "<provider>/<name>"; a kind absent takes any field.
	Fields map[string][]string
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

func (k Kinds) Validate(spec draft.KindSpec, payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return fmt.Errorf("%w: the payload of %s/%s is not a JSON object", outbox.ErrInvalid, spec.Provider, spec.Name)
	}
	allowed, ok := k.Fields[spec.Provider+"/"+spec.Name]
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
