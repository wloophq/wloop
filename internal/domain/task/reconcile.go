package task

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const itemOpen items.State = "open"

// Reconcile is what the items of one unit call for, given the tasks that hold them or the unit.
//
// One task per task key (items.Item.TaskKey, else the item id) of the open items that wait on Me, or that Me
// requested or f.Cares about and wait on others. An item a task already holds stays with that task, so a task
// the user merged keeps its items, and every item of a key goes to the task holding one of them. A key no task
// holds that names f.Unit goes to the unit's own task (UnitTask) while it is open, else to the oldest open task
// holding f.Unit as a link, such as a task you started whose pull request was attached, else to its own closed
// one; any other gets a new task, DerivedID of the key, in the first state of the category its first item
// proposes (items.Item.Initial), else the inbox for an item waiting on Me and the waiting category for one
// waiting on others. A new task takes the item's contexts and deadline in the properties of RoleContexts and
// RoleDeadline, and the kind f.Kind names, as the plugin writes them.
//
// A derived task none of whose items calls for it any more is resolved into the done category, except the
// task of a key that names f.Unit: the unit's one task lives as long as its unit, so it ends through the
// profile, and is dropped once its card says the user no longer Cares about the unit (PropUnfollowed), to come
// back when they do again. An item of that key that waits on nobody keeps the task while f Cares. A task that was clarified never returns to
// the inbox: an item that opens again on a done task moves it back into the ready category with Changed set,
// and an open task keeps its state. A task the user dropped stays dropped.
//
// Never changes the state, the parent or a property the user set, except to resolve and to reopen as said.
//
// Due: items.Item.Deadline in RFC 3339 is taken as is; "YYYY-MM-DD" is 18:00 in now's location that day;
// anything else is none.
func Reconcile(f UnitFacts, its []items.Item, me Me, have []Task, st Profile, now time.Time) ([]Change, error) {
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
	keyed := keyHolders(f, its, holder, byID, order)
	wanted := map[ID][]items.Item{}
	initials := map[ID]Category{}
	var created []ID
	var out []Change
	for _, it := range its {
		initial, ok := wants(f, it, me)
		if !ok {
			continue
		}
		id, held := holder[refOf(f, it)]
		if it.TaskKey != "" {
			id, held = keyed[it.TaskKey]
		}
		if !held {
			id = DerivedID(f.Unit, keyOf(it))
		}
		if _, known := byID[id]; !known && len(wanted[id]) == 0 {
			created = append(created, id)
			initials[id] = initial
		}
		wanted[id] = append(wanted[id], it)
	}
	for _, id := range created {
		e, err := newTask(id, f, wanted[id], initials[id], st, now)
		if err != nil {
			return nil, err
		}
		out = append(out, Change{Op: OpCreate, Task: e, Why: "derived from " + f.Unit.String()})
	}
	own, hasOwn := keyed[f.Unit.String()]
	for _, id := range order {
		old := byID[id]
		e, op, why := old, OpUpdate, ""
		var err error
		switch ws, ok := wanted[id]; {
		case ok:
			e, why, err = update(old, f, ws, st, now)
		case hasOwn && id == own:
			e.Items, e.Resolved = unheld(old, f.Unit)
			why = "no item calls for it"
			if old.Open() && old.Origin == OriginDerived && f.Carded && !f.Cares {
				op, why = OpResolve, "its unit is no longer followed"
				err = e.MoveTo(CategoryDropped, st)
				e.SetProp(PropUnfollowed, Value{Time: now}, WriterPlugin, now)
			}
		case old.Origin == OriginDerived && slices.Contains(old.Units, f.Unit) && len(old.Items)+len(old.Resolved) > 0:
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

func keyOf(it items.Item) string { return cmp.Or(it.TaskKey, it.ID) }

// keyHolders is the task of each task key the items its set: the task holding one of the key's items, an
// open one first, else, for the key that names f.Unit, the unit's own open task (UnitTask), else the oldest
// open task that holds f.Unit as a link, else its own closed one.
func keyHolders(f UnitFacts, its []items.Item, holder map[ItemRef]ID, byID map[ID]Task, order []ID) map[string]ID {
	out := map[string]ID{}
	for _, it := range its {
		if it.TaskKey == "" {
			continue
		}
		id, ok := holder[refOf(f, it)]
		if !ok {
			continue
		}
		if cur, has := out[it.TaskKey]; !has || !byID[cur].Open() && byID[id].Open() {
			out[it.TaskKey] = id
		}
	}
	own := f.Unit.String()
	if _, ok := out[own]; ok || !slices.ContainsFunc(its, func(it items.Item) bool { return it.TaskKey == own }) {
		return out
	}
	ts := make([]Task, 0, len(order))
	for _, id := range order {
		ts = append(ts, byID[id])
	}
	e, ok := UnitTask(ts, f.Unit)
	if l, linked := linking(ts, f.Unit); linked && (!ok || !e.Open()) {
		e, ok = l, true
	}
	if ok {
		out[own] = e.ID
	}
	return out
}

// linking is the oldest open task that holds u after its first unit, such as a task you started that its pull
// request was attached to.
func linking(ts []Task, u unit.Unit) (Task, bool) {
	var first *Task
	for i, e := range ts {
		if !e.Open() || len(e.Units) < 2 || !slices.Contains(e.Units[1:], u) {
			continue
		}
		if first == nil || cmp.Or(e.Created.Compare(first.Created), strings.Compare(string(e.ID), string(first.ID))) < 0 {
			first = &ts[i]
		}
	}
	if first == nil {
		return Task{}, false
	}
	return *first, true
}

// UnitTask is u's own task among ts, whatever its state: of the tasks whose first unit is u, an open one
// first, the one holding an item of u, then the oldest, then by id; with none open, the one updated last.
func UnitTask(ts []Task, u unit.Unit) (Task, bool) {
	holds := func(e Task) bool { return slices.ContainsFunc(e.Items, func(r ItemRef) bool { return r.Unit == u }) }
	better := func(e, than Task) bool {
		if e.Open() != than.Open() {
			return e.Open()
		}
		if !e.Open() {
			return cmp.Or(than.Updated.Compare(e.Updated), strings.Compare(string(e.ID), string(than.ID))) < 0
		}
		return cmp.Or(compareBool(holds(than), holds(e)), e.Created.Compare(than.Created), strings.Compare(string(e.ID), string(than.ID))) < 0
	}
	var first *Task
	for i, e := range ts {
		if len(e.Units) == 0 || e.Units[0] != u {
			continue
		}
		if first == nil || better(e, *first) {
			first = &ts[i]
		}
	}
	if first == nil {
		return Task{}, false
	}
	return *first, true
}

// compareBool is -1 when only b holds, 1 when only a does.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// unheld is what old holds once no item of u calls for it: those items move to its resolved ones.
func unheld(old Task, u unit.Unit) (held, resolved []ItemRef) {
	here := slices.DeleteFunc(slices.Clone(old.Items), func(r ItemRef) bool { return r.Unit != u })
	held = slices.DeleteFunc(slices.Clone(old.Items), func(r ItemRef) bool { return r.Unit == u })
	if len(held) == 0 {
		held = nil
	}
	return held, settle(old.Resolved, here...)
}

// wants says whether item it calls for a task, and the category a new task of it starts in.
func wants(f UnitFacts, it items.Item, me Me) (Category, bool) {
	if it.State != itemOpen {
		return "", false
	}
	start, ok := CategoryInbox, true
	if !slices.ContainsFunc(it.WaitingOn, func(p items.Person) bool { return isMe(p, me) }) {
		own := it.TaskKey == f.Unit.String() && len(it.WaitingOn) == 0
		start, ok = CategoryWaiting, (len(it.WaitingOn) > 0 || own) && (isMe(it.Requester, me) || f.Cares)
	}
	if it.Initial != "" {
		start = Category(it.Initial)
	}
	return start, ok
}

func newTask(id ID, f UnitFacts, ws []items.Item, initial Category, st Profile, now time.Time) (Task, error) {
	e := Task{ID: id, Title: ws[0].Summary, Origin: OriginDerived, Created: now, Updated: now}
	for _, it := range ws {
		e.Items = append(e.Items, refOf(f, it))
		e.Units = mergeUnits(e.Units, unitsOf(f, it))
	}
	sortRefs(e.Items)
	if err := e.MoveTo(initial, st); err != nil {
		return e, err
	}
	e.SetRole(st, RoleDeadline, Value{Time: deadline(ws, now.Location())}, WriterPlugin, now)
	e.SetRole(st, RoleContexts, Value{List: slices.Clone(ws[0].Contexts)}, WriterPlugin, now)
	e.SetProp(PropKind, Value{Text: f.Kind}, WriterPlugin, now)
	return e, nil
}

// update refreshes what the items ws own of old, which holds them or is their derived task, and keeps what
// the user set: old holds of f.Unit's items exactly ws, and keeps the others it held as Resolved. A task a gate
// opened on the unit before its items came becomes the derived task of the unit's key, and a unit's own task
// reconcile dropped once nobody followed the unit comes back into the ready category, Changed, when it is
// followed again. An item old does
// not hold opened again: a done task moves back into the ready category, and any task is marked Changed.
func update(old Task, f UnitFacts, ws []items.Item, st Profile, now time.Time) (Task, string, error) {
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
	if old.Origin == OriginGate && slices.ContainsFunc(ws, func(it items.Item) bool { return it.TaskKey != "" }) {
		e.Origin = OriginDerived
	}
	if e.Origin == OriginDerived && len(e.Items) == 1 {
		e.Title = ws[0].Summary
	}
	if due, _ := st.PropOf(RoleDeadline); due != "" && e.Written[due].By != WriterPerson {
		e.SetProp(due, Value{Time: deadline(ws, now.Location())}, WriterPlugin, now)
	}
	refollowed := old.Category == CategoryDropped && !old.Prop(PropUnfollowed).Time.IsZero() && f.Cares
	if refollowed {
		e.SetProp(PropUnfollowed, Value{}, WriterPlugin, now)
		if err := e.MoveTo(CategoryReady, st); err != nil {
			return e, "", err
		}
		e.Changed = true
		return e, "its unit is followed again", nil
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
