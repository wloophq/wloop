package task

import (
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const itemOpen items.State = "open"

// Reconcile is what the items of one unit call for, given the tasks that hold them or the unit.
//
// One task per open item that waits on Me, or that Me requested or f.Cares about and waits on others. An item
// a task already holds stays with that task, so a task the user merged keeps its items; one no task holds gets
// a new task, DerivedID, in the first state of the category the item proposes (items.Item.Initial), else the
// inbox for an item waiting on Me and the waiting category for one waiting on others. A new task takes the
// item's contexts and deadline, and the kind f.Kind names, as the plugin writes them.
//
// A derived task none of whose items calls for it any more is resolved into the done category. A task that
// was clarified never returns to the inbox: an item that opens again on a done task moves it back into the
// ready category with Changed set, and an open task keeps its state. A task the user dropped stays dropped.
//
// Never changes the state, the parent or a property the user set, except to resolve and to reopen as said.
//
// Due: items.Item.Deadline in RFC 3339 is taken as is; "YYYY-MM-DD" is 18:00 in now's location that day;
// anything else is none.
func Reconcile(f UnitFacts, its []items.Item, me Me, have []Task, st States, now time.Time) ([]Change, error) {
	byID := map[ID]Task{}
	holder := map[ItemRef]ID{}
	var order []ID
	for _, e := range have {
		if _, dup := byID[e.ID]; dup {
			continue
		}
		byID[e.ID], order = e, append(order, e.ID)
		for _, r := range slices.Concat(e.Items, e.Resolved) {
			if old, ok := holder[r]; !ok || !byID[old].Open() && e.Open() {
				holder[r] = e.ID
			}
		}
	}
	wanted := map[ID][]items.Item{}
	var out []Change
	for _, it := range its {
		initial, ok := wants(f, it, me)
		if !ok {
			continue
		}
		id, held := holder[refOf(f, it)]
		if !held {
			id = DerivedID(f.Unit, it.ID)
		}
		if _, known := byID[id]; !known && len(wanted[id]) == 0 {
			e, err := newTask(id, f, it, initial, st, now)
			if err != nil {
				return nil, err
			}
			out = append(out, Change{Op: OpCreate, Task: e, Why: "derived from " + f.Unit.String()})
		}
		wanted[id] = append(wanted[id], it)
	}
	for _, id := range order {
		old := byID[id]
		e, op, why := old, OpUpdate, ""
		var err error
		switch ws, ok := wanted[id]; {
		case ok:
			e, why, err = update(old, f, ws, st, now)
		case old.Origin == OriginDerived && slices.Contains(old.Units, f.Unit):
			e.Items, e.Resolved, why = nil, settle(old.Resolved, old.Items...), "no item calls for it"
			if old.Open() {
				op, why = OpResolve, "its items resolved"
				err = e.MoveTo(CategoryDone, st)
			}
		}
		if err != nil {
			return nil, err
		}
		if !e.Same(old) {
			e.Updated = now
			out = append(out, Change{Op: op, Task: e, Why: why})
		}
	}
	return out, nil
}

// wants says whether item it calls for a task, and the category a new task of it starts in.
func wants(f UnitFacts, it items.Item, me Me) (Category, bool) {
	if it.State != itemOpen {
		return "", false
	}
	start, ok := CategoryInbox, true
	if !slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return isMe(p, me) }) {
		start, ok = CategoryWaiting, len(it.WaitingOn) > 0 && (isMe(it.Requester, me) || f.Cares)
	}
	if it.Initial != "" {
		start = Category(it.Initial)
	}
	return start, ok
}

func newTask(id ID, f UnitFacts, it items.Item, initial Category, st States, now time.Time) (Task, error) {
	e := Task{ID: id, Title: it.Summary, Items: []ItemRef{refOf(f, it)}, Units: unitsOf(f, it),
		Origin: OriginDerived, Created: now, Updated: now}
	if err := e.MoveTo(initial, st); err != nil {
		return e, err
	}
	e.SetProp(PropDue, Value{Time: deadline([]items.Item{it}, now.Location())}, WriterPlugin, now)
	e.SetProp(PropContexts, Value{List: slices.Clone(it.Contexts)}, WriterPlugin, now)
	e.SetProp(PropKind, Value{Text: f.Kind}, WriterPlugin, now)
	return e, nil
}

// update refreshes what the items ws own of old, which holds them or is their derived task, and keeps what
// the user set: old holds of f.Unit's items exactly ws, and keeps the others it held as Resolved. An item old does
// not hold opened again: a done task moves back into the ready category, and any task is marked Changed.
func update(old Task, f UnitFacts, ws []items.Item, st States, now time.Time) (Task, string, error) {
	e := old
	e.Items = slices.DeleteFunc(slices.Clone(old.Items), func(r ItemRef) bool { return r.Unit == f.Unit })
	reopened := false
	var calling []ItemRef
	for _, it := range ws {
		r := refOf(f, it)
		calling = append(calling, r)
		if !slices.Contains(old.Items, r) {
			reopened = true
		}
		if !slices.Contains(e.Items, r) {
			e.Items = append(e.Items, r)
		}
		e.Units = mergeUnits(e.Units, unitsOf(f, it))
	}
	here := slices.DeleteFunc(slices.Clone(old.Items), func(r ItemRef) bool { return r.Unit != f.Unit })
	e.Resolved = settle(slices.DeleteFunc(settle(old.Resolved, here...), func(r ItemRef) bool { return slices.Contains(calling, r) }))
	sortRefs(e.Items)
	if old.Origin == OriginDerived && len(e.Items) == 1 {
		e.Title = ws[0].Summary
	}
	if e.Written[PropDue].By != WriterPerson {
		e.SetProp(PropDue, Value{Time: deadline(ws, now.Location())}, WriterPlugin, now)
	}
	switch {
	case !reopened || old.Category == CategoryDropped:
		return e, "items changed", nil
	case old.Category == CategoryDone:
		if err := e.MoveTo(CategoryReady, st); err != nil {
			return e, "", err
		}
	}
	e.Changed = true
	return e, "an item reopened", nil
}

func refOf(f UnitFacts, it items.Item) ItemRef {
	u := it.Unit
	if u.IsZero() {
		u = f.Unit
	}
	return ItemRef{Unit: u, Item: it.ID}
}

func deadline(its []items.Item, loc *time.Location) time.Time {
	var d time.Time
	for _, it := range its {
		if t, ok := ParseDeadline(it.Deadline, loc); ok && (d.IsZero() || t.Before(d)) {
			d = t
		}
	}
	return d
}

func unitsOf(f UnitFacts, it items.Item) []unit.Unit {
	out := []unit.Unit{f.Unit}
	if !it.Unit.IsZero() && it.Unit != f.Unit {
		out = append(out, it.Unit)
	}
	return out
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

// settle is resolved with refs added, each once and sorted.
func settle(resolved []ItemRef, refs ...ItemRef) []ItemRef {
	out := slices.Clone(resolved)
	for _, r := range refs {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	sortRefs(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
