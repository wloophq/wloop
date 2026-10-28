package task

import (
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const itemOpen items.State = "open"

// want is an entry the items call for.
type want struct {
	id      ID
	kind    Kind
	title   string
	items   []items.Item
	initial Category
	person  *items.Person
}

// Reconcile is what the items of one unit call for, given the entries that point at them.
// Rules (spec 5.2): an open item with Me in WaitingOn -> KindAction, created in the state the item proposes
// (items.Item.Initial: "inbox" or "next", the inbox or the ready category; "" is inbox) with its contexts; an
// existing entry keeps the user's state;
// open items owed by others with Me as requester or Cares -> one KindWaiting per (unit, waiting person);
// StartedByMe && all items resolved && !ClosurePosted -> KindClose action; a resolved item drops from its entry,
// an entry with all items resolved -> done. Only items create entries. Never changes the state, the parent or
// a property set by the user except to done (resolution), inbox (activity) and deferred (mute).
// An entry the user dismissed or finished is not recreated for the same items until one of them
// reopens: an open item the entry does not hold returns it to the inbox.
// Mute: with f.Mute set, every open entry of the unit (new or existing) is deferred with
// Snooze{Until: Mute.Until, OnActivity: Mute.UntilStateChange, Return: its state, Mute: true}; when the mute ends
// (f.Mute nil) an entry with Snooze.Mute returns to Snooze.Return. A muted entry never enters Inbox on its own.
// Due: items.Item.Deadline in RFC 3339 is taken as is; "YYYY-MM-DD" is 18:00 in now's location that day;
// anything else is none.
func Reconcile(f UnitFacts, its []items.Item, me Me, have []Task, st States, now time.Time) ([]Change, error) {
	mute := activeMute(f, now)
	wants := wanted(f, its, me)
	byID := map[ID]Task{}
	for _, e := range have {
		if e.Origin == OriginDerived {
			byID[e.ID] = e
		}
	}
	var out []Change
	seen := map[ID]bool{}
	for _, w := range wants {
		seen[w.id] = true
		old, ok := byID[w.id]
		if !ok {
			e, err := w.task(f, st, now)
			if err == nil {
				e, err = applyMute(e, mute, st)
			}
			if err != nil {
				return nil, err
			}
			out = append(out, Change{Op: OpCreate, Task: e, Why: "derived from " + f.Unit.String()})
			continue
		}
		e, op, why, err := w.update(old, f, st, now)
		if err == nil {
			e, err = applyMute(e, mute, st)
		}
		if err != nil {
			return nil, err
		}
		if !e.Same(old) {
			e.Updated = now
			out = append(out, Change{Op: op, Task: e, Why: why})
		}
	}
	for _, old := range have {
		if old.Origin != OriginDerived || seen[old.ID] {
			continue
		}
		if _, mine := byID[old.ID]; !mine || !derivedFrom(old, f) {
			continue
		}
		e := old
		e.Items = nil
		op, why := OpUpdate, "no item calls for it"
		if e.Open() {
			if err := e.MoveTo(CategoryDone, st); err != nil {
				return nil, err
			}
			op, why = OpResolve, "its items resolved"
			e.Snooze = Snooze{}
		}
		if !e.Same(old) {
			e.Updated = now
			out = append(out, Change{Op: op, Task: e, Why: why})
		}
	}
	return out, nil
}

func derivedFrom(e Task, f UnitFacts) bool {
	return slices.Contains(e.Units, f.Unit)
}

func wanted(f UnitFacts, its []items.Item, me Me) []want {
	var out []want
	waiting := map[string]*want{}
	var order []string
	allResolved := len(its) > 0
	for _, it := range its {
		if it.State != itemOpen {
			continue
		}
		allResolved = false
		if slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return isMe(p, me) }) {
			out = append(out, want{id: DerivedID(KindAction, f.Unit, it.ID), kind: KindAction, title: it.Summary,
				items: []items.Item{it}, initial: initialState(it.Initial)})
			continue
		}
		if !isMe(it.Requester, me) && !f.Cares {
			continue
		}
		for _, p := range it.WaitingOn {
			w, ok := waiting[p.Key]
			if !ok {
				p := p
				w = &want{id: DerivedID(KindWaiting, f.Unit, p.Key), kind: KindWaiting, initial: CategoryWaiting, person: &p}
				waiting[p.Key] = w
				order = append(order, p.Key)
			}
			w.items = append(w.items, it)
		}
	}
	for _, k := range order {
		w := waiting[k]
		w.title = waitingTitle(*w.person, w.items)
		out = append(out, *w)
	}
	if f.StartedByMe && allResolved && !f.ClosurePosted {
		out = append(out, want{id: DerivedID(KindClose, f.Unit, ""), kind: KindClose, initial: CategoryInbox,
			title: "Close " + f.Unit.Label()})
	}
	return out
}

func initialState(i items.Initial) Category {
	if i == items.InitialNext {
		return CategoryReady
	}
	return CategoryInbox
}

func waitingTitle(p items.Person, its []items.Item) string {
	if len(its) == 1 {
		return its[0].Summary
	}
	name := p.Name
	if name == "" {
		name = p.Key
	}
	return fmt.Sprintf("%s owes %d items", name, len(its))
}

func (w want) refs() []ItemRef {
	var out []ItemRef
	for _, it := range w.items {
		out = append(out, ItemRef{Unit: it.Unit, Item: it.ID})
	}
	sortRefs(out)
	return out
}

func (w want) deadline(loc *time.Location) time.Time {
	var d time.Time
	for _, it := range w.items {
		if t, ok := ParseDeadline(it.Deadline, loc); ok && (d.IsZero() || t.Before(d)) {
			d = t
		}
	}
	return d
}

func (w want) since(now time.Time) time.Time {
	var s time.Time
	for _, it := range w.items {
		if !it.CreatedAt.IsZero() && (s.IsZero() || it.CreatedAt.Before(s)) {
			s = it.CreatedAt
		}
	}
	if s.IsZero() {
		return now
	}
	return s
}

func (w want) task(f UnitFacts, st States, now time.Time) (Task, error) {
	e := Task{ID: w.id, Kind: w.kind, Title: w.title, Items: w.refs(), Units: units(f, w),
		Origin: OriginDerived, Created: now, Updated: now}
	if err := e.MoveTo(w.initial, st); err != nil {
		return e, err
	}
	e.SetProp(PropDue, Value{Time: w.deadline(now.Location())}, WriterPlugin, now)
	if w.kind == KindAction {
		e.SetProp(PropContexts, Value{List: slices.Clone(w.items[0].Contexts)}, WriterPlugin, now)
	}
	if w.person != nil {
		e.Waiting = &WaitingOn{On: []items.Person{*w.person}, Since: w.since(now)}
		e.SetProp(PropContexts, Value{List: contextsOf(w.items)}, WriterPlugin, now)
	}
	return e, nil
}

func contextsOf(its []items.Item) []string {
	var out []string
	for _, it := range its {
		for _, c := range it.Contexts {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

func units(f UnitFacts, w want) []unit.Unit {
	out := []unit.Unit{f.Unit}
	for _, it := range w.items {
		if !it.Unit.IsZero() && !slices.Contains(out, it.Unit) {
			out = append(out, it.Unit)
		}
	}
	return out
}

// update refreshes what the items own of old and keeps what the user set; a held entry that gains an
// open item it did not hold returns to Inbox.
func (w want) update(old Task, f UnitFacts, st States, now time.Time) (Task, ChangeOp, string, error) {
	e := old
	refs := w.refs()
	e.Title, e.Items = w.title, refs
	if e.Written[PropDue].By != WriterPerson {
		e.SetProp(PropDue, Value{Time: w.deadline(now.Location())}, WriterPlugin, now)
	}
	e.Units = mergeUnits(old.Units, units(f, w))
	if w.person != nil {
		on := []items.Person{*w.person}
		if old.Waiting == nil {
			e.Waiting = &WaitingOn{On: on, Since: w.since(now)}
		} else {
			wo := *old.Waiting
			wo.On = on
			e.Waiting = &wo
		}
	}
	if !old.Open() && reopened(old.Items, refs) {
		if err := e.MoveTo(CategoryInbox, st); err != nil {
			return e, "", "", err
		}
		e.Changed, e.Snooze = true, Snooze{}
		return e, OpReinbox, "an item reopened", nil
	}
	return e, OpUpdate, "items changed", nil
}

func reopened(held, now []ItemRef) bool {
	for _, r := range now {
		if !slices.Contains(held, r) {
			return true
		}
	}
	return false
}

func mergeUnits(a, b []unit.Unit) []unit.Unit {
	out := slices.Clone(a)
	for _, u := range b {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}
