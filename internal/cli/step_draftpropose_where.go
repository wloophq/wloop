package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// readers are the person keys of the members of a unit's conversation, as its plugin's members fact holds
// them; known is false while the plugin cannot tell. Only the provider's own person keys are judged: a group
// or a team named by text is neither in nor out.
type readers struct {
	provider unit.ProviderID
	keys     []string
	known    bool
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
	r := readers{provider: u.Provider}
	fact := d.membersFact(u.Provider)
	if fact == "" {
		return r
	}
	switch v := facts[fact].(type) {
	case []string:
		r.keys, r.known = v, true
	case []any:
		r.known = true
		for _, k := range v {
			if s, ok := k.(string); ok {
				r.keys = append(r.keys, s)
			}
		}
	}
	return r
}

// unitReaders are the readers of u read through the deps' Facts; none known when it reads none or u is gone.
func (d *draftDeps) unitReaders(ctx context.Context, u unit.Unit) (readers, error) {
	if d.Facts == nil || d.membersFact(u.Provider) == "" {
		return readers{provider: u.Provider}, nil
	}
	facts, err := d.Facts(ctx, u)
	switch {
	case errors.Is(err, automation.ErrUnitGone):
		return readers{provider: u.Provider}, nil
	case err != nil:
		return readers{}, err
	}
	return d.readersOf(u, facts), nil
}

// reads says whether p is one of the readers, and whether that is known: it is not while the members are
// unknown or p's key is not one of the provider's own.
func (r readers) reads(p items.Person, codecs unit.KeyComparer) (in, known bool) {
	if !r.known || !strings.HasPrefix(p.Key, string(r.provider)+":") {
		return false, false
	}
	return p.Is(items.Identity{Keys: r.keys, Codecs: codecs}), true
}

// outside counts the people known not to be readers; none for a draft whose target is the one person it is
// to, which posts in their direct conversation.
func (r readers) outside(people []string, target string, codecs unit.KeyComparer) int {
	if inTheirOwn(people, target) {
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

// joined says whether the one person a draft posted in their direct conversation is to is now known to read
// the conversation it came from.
func (r readers) joined(people []string, target string, codecs unit.KeyComparer) bool {
	if !inTheirOwn(people, target) {
		return false
	}
	in, known := r.reads(items.Person{Key: target}, codecs)
	return known && in
}

func inTheirOwn(people []string, target string) bool {
	return target != "" && len(people) == 1 && people[0] == target
}

// logUnplaced logs, once each time who can read u turns unknown, that workflow's step addresses people
// there as before; unknown says whether it is unknown now.
func (s *stepSet) logUnplaced(workflow string, u unit.Unit, unknown bool) {
	key := workflow + " " + u.String()
	s.unplacedMu.Lock()
	defer s.unplacedMu.Unlock()
	if s.unplaced[key] == unknown {
		return
	}
	if s.unplaced == nil {
		s.unplaced = map[string]bool{}
	}
	s.unplaced[key] = unknown
	if unknown {
		s.logf("draft.propose %s: who can read %s is unknown; nudging its people there as before", workflow, u)
	}
}

// addressed says whether a step whose to is to addresses p among r: a person whose membership is not known
// is addressed in the conversation as before and never written to elsewhere; unknown says so.
func (r readers) addressed(to string, p items.Person, codecs unit.KeyComparer) (ok, unknown bool) {
	if to == "" || to == builtin.DraftTo[0] {
		return true, false
	}
	in, known := r.reads(p, codecs)
	if !known {
		return to != builtin.DraftTo[2], true
	}
	return in == (to == builtin.DraftTo[1]), false
}
