// Package links resolves linked items from events on the units they link to.
package links

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	ditems "github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type EventReader interface {
	UnitEvents(ctx context.Context, u unit.Unit, since time.Time) ([]link.Event, error)
}

type Resolver struct {
	Links  port.LinkStore
	Events map[unit.ProviderID]EventReader // from source.Module.UnitEvents of enabled plugins
	Ledger items.Applier
	Clock  port.Clock
	Log    func(string, ...any)
	// Codecs compare an actor's key with an assignee's; nil compares them byte for byte.
	Codecs unit.KeyComparer

	mu sync.Mutex
	// silent is when each target was last logged for each reason.
	silent map[silentKey]time.Time
}

type silentKey struct {
	target unit.Unit
	why    string
}

const silentEvery = 24 * time.Hour

// pending is an open item a link may resolve, with the time after which a
// review on its target counts, and the time after which its target merging
// or closing does: a pull request that ended before the link was noted
// still settles it, unless you touched the item since.
type pending struct {
	link  link.Link
	item  ditems.Item
	after time.Time
	ended time.Time
}

// Run resolves the open linked items whose link targets are in targets.
func (r *Resolver) Run(ctx context.Context, targets []unit.Unit) (resolved int, err error) {
	ls, err := r.Links.To(ctx, targets)
	if err != nil {
		return 0, fmt.Errorf("links to %d units: %w", len(targets), err)
	}
	byTarget := map[unit.Unit][]link.Link{}
	var order []unit.Unit
	for _, l := range ls {
		if l.Item == "" || (l.Kind != link.Review && l.Kind != link.Merge) {
			continue
		}
		if _, seen := byTarget[l.To]; !seen {
			order = append(order, l.To)
		}
		byTarget[l.To] = append(byTarget[l.To], l)
	}
	slices.SortFunc(order, compareUnits)
	itemsOf := map[unit.Unit][]ditems.Item{}
	var errs []error
	for _, target := range order {
		open, err := r.open(ctx, byTarget[target], itemsOf)
		if err != nil {
			errs = append(errs, err)
		}
		if len(open) == 0 {
			continue
		}
		n, err := r.resolve(ctx, target, open)
		resolved += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return resolved, errors.Join(errs...)
}

// open are the links of ls whose item is open, read through itemsOf.
func (r *Resolver) open(ctx context.Context, ls []link.Link, itemsOf map[unit.Unit][]ditems.Item) ([]pending, error) {
	var out []pending
	var errs []error
	for _, l := range ls {
		its, ok := itemsOf[l.From]
		if !ok {
			var err error
			if its, err = r.Ledger.Items(ctx, l.From); err != nil {
				errs = append(errs, fmt.Errorf("items of %s: %w", l.From, err))
			}
			itemsOf[l.From] = its
		}
		i := slices.IndexFunc(its, func(it ditems.Item) bool { return it.ID == l.Item })
		if i < 0 || its[i].State != ditems.Open {
			continue
		}
		after := l.At
		if its[i].UserAt.After(after) {
			after = its[i].UserAt
		}
		out = append(out, pending{link: l, item: its[i], after: after, ended: its[i].UserAt})
	}
	return out, errors.Join(errs...)
}

func (r *Resolver) resolve(ctx context.Context, target unit.Unit, open []pending) (int, error) {
	reader := r.Events[target.Provider]
	if reader == nil {
		r.quiet(target, "has no events")
		return 0, nil
	}
	since := open[0].ended
	for _, p := range open[1:] {
		if p.ended.Before(since) {
			since = p.ended
		}
	}
	evs, err := reader.UnitEvents(ctx, target, since)
	if err != nil {
		return 0, fmt.Errorf("events of %s: %w", target, err)
	}
	slices.SortStableFunc(evs, func(a, b link.Event) int { return a.At.Compare(b.At) })
	resolved := 0
	var errs []error
	for _, p := range open {
		ev, res, ok := settle(p, evs, r.Codecs)
		if !ok {
			continue
		}
		e := ditems.Evidence{Kind: ditems.EvidenceUnitEvent, Anchor: ev.Anchor}
		op := ditems.Op{Op: ditems.OpResolve, Unit: p.link.From, Item: p.link.Item, Anchor: ev.Anchor, Resolution: res, Evidence: &e,
			Reason: fmt.Sprintf("%s link: %s on %s", p.link.Kind, ev.Kind, target)}
		got, err := r.Ledger.Apply(ctx, ditems.Derived, []ditems.Op{op})
		var disabled *items.DisabledError
		switch {
		case errors.As(err, &disabled):
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("resolve %s %s: %w", p.link.From, p.link.Item, err))
			continue
		}
		for _, a := range got.Applied {
			if a.Changed && a.Item.State == ditems.Resolved {
				resolved++
			}
		}
	}
	return resolved, errors.Join(errs...)
}

// settle is the first event of evs, oldest first, that settles p's item, and
// the resolution it gives.
func settle(p pending, evs []link.Event, codecs unit.KeyComparer) (link.Event, ditems.Resolution, bool) {
	for _, ev := range evs {
		terminal := ev.Kind == link.EventClosed || ev.Kind == link.EventMerged
		if !ev.At.After(p.after) && !(terminal && ev.At.After(p.ended)) {
			continue
		}
		switch {
		case ev.Kind == link.EventClosed:
			return ev, ditems.Moot, true
		case ev.Kind == link.EventMerged && p.link.Kind == link.Merge:
			return ev, ditems.Done, true
		case ev.Kind == link.EventMerged:
			return ev, ditems.Moot, true
		case ev.Kind == link.EventReview && p.link.Kind == link.Review && byAssignee(p.item, ev.Actor, codecs):
			return ev, ditems.Done, true
		}
	}
	return link.Event{}, "", false
}

// byAssignee says whether actor is an assignee of it; anyone is when it has none.
func byAssignee(it ditems.Item, actor string, codecs unit.KeyComparer) bool {
	if len(it.Assignees) == 0 {
		return true
	}
	id := ditems.Identity{Keys: []string{actor}, Codecs: codecs}
	return slices.ContainsFunc(it.Assignees, func(p ditems.Person) bool { return p.Is(id) })
}

// quiet logs, once per target and reason per day, why a target cannot resolve
// anything: no reader for its provider. A target with no review, merge or
// close yet is waiting, which is not worth a line.
func (r *Resolver) quiet(target unit.Unit, why string) {
	if r.Log == nil {
		return
	}
	now := time.Now()
	if r.Clock != nil {
		now = r.Clock.Now()
	}
	r.mu.Lock()
	key := silentKey{target, why}
	last, logged := r.silent[key]
	if logged && now.Sub(last) < silentEvery {
		r.mu.Unlock()
		return
	}
	if r.silent == nil {
		r.silent = map[silentKey]time.Time{}
	}
	r.silent[key] = now
	r.mu.Unlock()
	r.Log("link target %s %s", target, why)
}

func compareUnits(a, b unit.Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}
