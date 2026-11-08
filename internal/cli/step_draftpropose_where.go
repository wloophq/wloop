package cli

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// readers are the person keys of the members of a unit's conversation, as its plugin's members fact holds
// them; known is false while the plugin cannot tell.
type readers struct {
	keys  []string
	known bool
}

// membersFact is the fact that holds who reads p's units' conversations; "" for none.
func (d *draftDeps) membersFact(p unit.ProviderID) string {
	if d.Members != nil {
		return d.Members[p]
	}
	return builtin.MembersFacts[p]
}

// readersOf are the readers facts, u's, tell through its provider's members fact.
func (d *draftDeps) readersOf(u unit.Unit, facts workflow.Facts) readers {
	fact := d.membersFact(u.Provider)
	if fact == "" {
		return readers{}
	}
	switch v := facts[fact].(type) {
	case []string:
		return readers{keys: v, known: true}
	case []any:
		r := readers{known: true}
		for _, k := range v {
			if s, ok := k.(string); ok {
				r.keys = append(r.keys, s)
			}
		}
		return r
	}
	return readers{}
}

// unitReaders are the readers of u read through the deps' Facts; none known when it reads none or u is gone.
func (d *draftDeps) unitReaders(ctx context.Context, u unit.Unit) (readers, error) {
	if d.Facts == nil || d.membersFact(u.Provider) == "" {
		return readers{}, nil
	}
	facts, err := d.Facts(ctx, u)
	switch {
	case errors.Is(err, automation.ErrUnitGone):
		return readers{}, nil
	case err != nil:
		return readers{}, err
	}
	return d.readersOf(u, facts), nil
}

// reads says whether p is one of the readers, and whether that is known.
func (r readers) reads(p items.Person, codecs unit.KeyComparer) (in, known bool) {
	if !r.known {
		return false, false
	}
	return p.Is(items.Identity{Keys: r.keys, Codecs: codecs}), true
}

// outside counts the people known not to be readers; none for a draft whose target is the one person it is
// to, which posts in their direct conversation.
func (r readers) outside(people []string, target string, codecs unit.KeyComparer) int {
	if target != "" && len(people) == 1 && people[0] == target {
		return 0
	}
	n := 0
	for _, p := range people {
		if in, known := r.reads(items.Person{Key: p}, codecs); known && !in {
			n++
		}
	}
	return n
}

// addressed says whether a step whose to is to addresses p among r; unknown says p is left out only because
// r is unknown.
func (r readers) addressed(to string, p items.Person, codecs unit.KeyComparer) (ok, unknown bool) {
	if to == "" || to == builtin.DraftTo[0] {
		return true, false
	}
	in, known := r.reads(p, codecs)
	if !known {
		return false, true
	}
	return in == (to == "members"), false
}
