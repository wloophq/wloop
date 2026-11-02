// Package links resolves linked items from events on the units they link to.
package links

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
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

// Refresher reads a unit from its source again when what the host holds of it may be stale.
type Refresher interface {
	// Refresh says whether it read u; a unit it holds fresh is not read.
	Refresh(ctx context.Context, u unit.Unit) (read bool, err error)
}

// RetryAfter is how long a target whose read failed waits before the next, RetryNotFoundAfter
// when its source has no such unit or will not show it. ReadEndedEvery is how often a target that
// ended, and still settles nothing, is read: only a reopening would change it.
const (
	RetryAfter         = time.Hour
	RetryNotFoundAfter = 24 * time.Hour
	ReadEndedEvery     = 24 * time.Hour
)

// ErrNotReady is a Refresher's answer while its source cannot read at all, such as a plugin still
// starting: Run fails with it, so its units run again soon, and the target waits for nothing.
var ErrNotReady = errors.New("the source cannot read yet")

type Resolver struct {
	Links  port.LinkStore
	Events map[unit.ProviderID]EventReader // from source.Module.UnitEvents of enabled plugins
	// Refresh reads a target an open item still waits on again, by provider; nil reads none.
	Refresh map[unit.ProviderID]Refresher
	Ledger  items.Applier
	Clock   port.Clock
	Log     func(string, ...any)
	// Codecs compare an actor's key with an assignee's; nil compares them byte for byte.
	Codecs unit.KeyComparer

	mu sync.Mutex
	// silent is when each target was last logged for each reason.
	silent map[silentKey]time.Time
	// next is until when each target read or tried waits before the next read.
	next map[unit.Unit]time.Time
}

type silentKey struct {
	target unit.Unit
	why    string
}

const silentEvery = 24 * time.Hour

// pending is an open item one of its links may settle, with the time after which a review, or an
// end on a review link, counts: the ask, or the user's last edit or the item's reopening when
// later. An end settles a merge link whenever it happened, unless the user edited the item or
// it reopened since.
type pending struct {
	link link.Link
	item ditems.Item
	from time.Time
	user time.Time
}

func pendingOf(l link.Link, it ditems.Item) pending {
	from := it.Origin.At
	if from.IsZero() {
		from = it.CreatedAt
	}
	user := it.UserAt
	if it.ReopenedAt.After(user) {
		user = it.ReopenedAt
	}
	if user.After(from) {
		from = user
	}
	return pending{link: l, item: it, from: from, user: user}
}

type itemKey struct {
	unit unit.Unit
	item string
}

// Run resolves the open items linked to a unit of targets once every review and merge link of each
// settles, reading a target such an item still waits on again through Refresh.
func (r *Resolver) Run(ctx context.Context, targets []unit.Unit) (resolved int, err error) {
	ls, err := r.Links.To(ctx, targets)
	if err != nil {
		return 0, fmt.Errorf("links to %d units: %w", len(targets), err)
	}
	var keys []itemKey
	for _, l := range ls {
		if settling(l) && !slices.Contains(keys, itemKey{l.From, l.Item}) {
			keys = append(keys, itemKey{l.From, l.Item})
		}
	}
	slices.SortFunc(keys, func(a, b itemKey) int {
		if c := compareUnits(a.unit, b.unit); c != 0 {
			return c
		}
		return strings.Compare(a.item, b.item)
	})
	run := &pass{r: r, events: map[unit.Unit][]link.Event{}, links: map[unit.Unit][]link.Link{}, items: map[unit.Unit][]ditems.Item{},
		read: map[unit.Unit]bool{}}
	var errs []error
	for _, k := range keys {
		n, err := run.resolve(ctx, k)
		resolved += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return resolved, errors.Join(errs...)
}

// pass is one Run's reads, so each target, unit's links and unit's items is read once.
type pass struct {
	r      *Resolver
	events map[unit.Unit][]link.Event
	links  map[unit.Unit][]link.Link
	items  map[unit.Unit][]ditems.Item
	read   map[unit.Unit]bool
}

func (p *pass) resolve(ctx context.Context, k itemKey) (int, error) {
	its, ok := p.items[k.unit]
	if !ok {
		var err error
		if its, err = p.r.Ledger.Items(ctx, k.unit); err != nil {
			return 0, fmt.Errorf("items of %s: %w", k.unit, err)
		}
		p.items[k.unit] = its
	}
	i := slices.IndexFunc(its, func(it ditems.Item) bool { return it.ID == k.item })
	if i < 0 || its[i].State != ditems.Open {
		return 0, nil
	}
	all, ok := p.links[k.unit]
	if !ok {
		var err error
		if all, err = p.r.Links.From(ctx, k.unit); err != nil {
			return 0, fmt.Errorf("links of %s: %w", k.unit, err)
		}
		p.links[k.unit] = all
	}
	ls := slices.DeleteFunc(slices.Clone(all), func(l link.Link) bool { return !settling(l) || l.Item != k.item })
	op, waits, ok, err := p.r.judge(ctx, ls, its[i], p.eventsOf)
	if err != nil {
		return 0, err
	}
	if !ok {
		read, err := p.refresh(ctx, waits)
		if err != nil {
			return 0, err
		}
		if read {
			if op, _, ok, err = p.r.judge(ctx, ls, its[i], p.eventsOf); err != nil {
				return 0, err
			}
		}
	}
	if !ok {
		return 0, nil
	}
	got, err := p.r.Ledger.Apply(ctx, ditems.Derived, []ditems.Op{op})
	var disabled *items.DisabledError
	switch {
	case errors.As(err, &disabled):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("resolve %s %s: %w", k.unit, k.item, err)
	}
	n := 0
	for _, a := range got.Applied {
		if a.Changed && a.Item.State == ditems.Resolved {
			n++
		}
	}
	return n, nil
}

func (p *pass) eventsOf(ctx context.Context, u unit.Unit) ([]link.Event, bool, error) {
	if evs, ok := p.events[u]; ok {
		return evs, true, nil
	}
	evs, ok, err := p.r.eventsOf(ctx, u)
	if err == nil && ok {
		p.events[u] = evs
	}
	return evs, ok, err
}

// refresh reads again each target of waits not read yet in this pass; true when it read any.
func (p *pass) refresh(ctx context.Context, waits []unit.Unit) (bool, error) {
	read := false
	for _, u := range waits {
		if p.read[u] {
			continue
		}
		p.read[u] = true
		got, err := p.r.refresh(ctx, u)
		if err != nil {
			return read, fmt.Errorf("read %s: %w", u, err)
		}
		if !got {
			continue
		}
		read = true
		delete(p.events, u)
		evs, _, err := p.eventsOf(ctx, u)
		if err != nil {
			return read, fmt.Errorf("events of %s: %w", u, err)
		}
		if slices.ContainsFunc(evs, func(ev link.Event) bool { return ev.Kind == link.EventMerged || ev.Kind == link.EventClosed }) {
			p.r.wait(u, ReadEndedEvery)
		}
	}
	return read, nil
}

// refresh reads u again unless it waits; an error is ErrNotReady's alone.
func (r *Resolver) refresh(ctx context.Context, u unit.Unit) (bool, error) {
	rf := r.Refresh[u.Provider]
	if rf == nil {
		return false, nil
	}
	r.mu.Lock()
	until := r.next[u]
	r.mu.Unlock()
	if r.now().Before(until) {
		return false, nil
	}
	read, err := rf.Refresh(ctx, u)
	switch {
	case err == nil:
		return read, nil
	case errors.Is(err, ErrNotReady):
		return false, err
	case ctx.Err() != nil:
		return false, nil
	case errors.Is(err, port.ErrNotFound):
		r.wait(u, RetryNotFoundAfter)
	default:
		r.wait(u, RetryAfter)
	}
	r.quiet(u, "cannot be read: "+err.Error())
	return false, nil
}

// wait makes u wait d before its next read, dropping the waits that passed.
func (r *Resolver) wait(u unit.Unit, d time.Duration) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.next == nil {
		r.next = map[unit.Unit]time.Time{}
	}
	maps.DeleteFunc(r.next, func(_ unit.Unit, until time.Time) bool { return !now.Before(until) })
	r.next[u] = now.Add(d)
}

// Settles is the op that settles it, open, through its review and merge links ls from the events their
// targets already have, as Run would apply it; ok is false while one of them does not settle. The items
// ledger calls it once a batch stored its links, so an item linked to pull requests that already ended
// resolves in the same batch, before any task holds it.
func (r *Resolver) Settles(ctx context.Context, ls []link.Link, it ditems.Item) (ditems.Op, bool, error) {
	ls = slices.DeleteFunc(slices.Clone(ls), func(l link.Link) bool { return !settling(l) || l.Item != it.ID })
	if len(ls) == 0 || it.State != ditems.Open {
		return ditems.Op{}, false, nil
	}
	op, _, ok, err := r.judge(ctx, ls, it, r.eventsOf)
	return op, ok, err
}

// judge is the op that resolves it once every link of ls settles: done when one settles done, else moot,
// with the latest settling event as its evidence; waits are the targets that do not settle yet.
func (r *Resolver) judge(ctx context.Context, ls []link.Link, it ditems.Item,
	read func(context.Context, unit.Unit) ([]link.Event, bool, error)) (op ditems.Op, waits []unit.Unit, ok bool, err error) {
	var last link.Event
	var lastLink pending
	res := ditems.Moot
	for _, l := range ls {
		evs, known, err := read(ctx, l.To)
		if err != nil {
			return ditems.Op{}, nil, false, fmt.Errorf("events of %s: %w", l.To, err)
		}
		if !known {
			return ditems.Op{}, nil, false, nil
		}
		p := pendingOf(l, it)
		ev, got, settled := settle(p, evs, r.Codecs)
		if !settled {
			waits = append(waits, l.To)
			continue
		}
		if got == ditems.Done {
			res = ditems.Done
		}
		if last.At.IsZero() || ev.At.After(last.At) {
			last, lastLink = ev, p
		}
	}
	if len(ls) == 0 || len(waits) > 0 {
		return ditems.Op{}, waits, false, nil
	}
	return resolveOp(lastLink, last, res, len(ls)), nil, true, nil
}

// eventsOf are u's events, oldest first; false when no reader knows its provider.
func (r *Resolver) eventsOf(ctx context.Context, u unit.Unit) ([]link.Event, bool, error) {
	reader := r.Events[u.Provider]
	if reader == nil {
		r.quiet(u, "has no events")
		return nil, false, nil
	}
	evs, err := reader.UnitEvents(ctx, u, time.Time{})
	if err != nil {
		return nil, false, err
	}
	slices.SortStableFunc(evs, func(a, b link.Event) int { return a.At.Compare(b.At) })
	return evs, true, nil
}

func resolveOp(p pending, ev link.Event, res ditems.Resolution, links int) ditems.Op {
	e := ditems.Evidence{Kind: ditems.EvidenceUnitEvent, Anchor: ev.Anchor}
	reason := fmt.Sprintf("%s link: %s on %s", p.link.Kind, ev.Kind, p.link.To)
	if links > 1 {
		reason += fmt.Sprintf(", the last of %d links to settle", links)
	}
	return ditems.Op{Op: ditems.OpResolve, Unit: p.link.From, Item: p.link.Item, Anchor: ev.Anchor, Resolution: res, Evidence: &e, Reason: reason}
}

func settling(l link.Link) bool {
	return l.Item != "" && (l.Kind == link.Review || l.Kind == link.Merge)
}

// settle is the first event of evs, oldest first, that settles p's link, and the resolution it gives.
func settle(p pending, evs []link.Event, codecs unit.KeyComparer) (link.Event, ditems.Resolution, bool) {
	for _, ev := range evs {
		ended := ev.Kind == link.EventMerged || ev.Kind == link.EventClosed
		switch {
		case !p.user.IsZero() && !ev.At.After(p.user):
		case ended && p.link.Kind == link.Merge:
			return ev, mergeResolution(ev.Kind), true
		case !ev.At.After(p.from):
		case ended:
			return ev, ditems.Moot, true
		case ev.Kind == link.EventReview && p.link.Kind == link.Review && byAssignee(p.item, ev.Actor, codecs):
			return ev, ditems.Done, true
		}
	}
	return link.Event{}, "", false
}

// mergeResolution is how an end of its target settles a merge link.
func mergeResolution(k link.EventKind) ditems.Resolution {
	if k == link.EventMerged {
		return ditems.Done
	}
	return ditems.Moot
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
// anything: no reader for its provider, or a failed read. A target with no review, merge or
// close yet is waiting, which is not worth a line.
func (r *Resolver) quiet(target unit.Unit, why string) {
	if r.Log == nil {
		return
	}
	now := r.now()
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

func (r *Resolver) now() time.Time {
	if r.Clock == nil {
		return time.Now()
	}
	return r.Clock.Now()
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
