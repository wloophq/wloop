package task

import (
	"cmp"
	"slices"
)

// ViewOpen selects every open task: its category is neither done nor dropped. It is not a served view and has no counter.
const ViewOpen View = "open"

// StoredViews are the views whose entries the store holds, in the order counters list them.
var StoredViews = []View{ViewFocus, ViewInbox, ViewNext, ViewWaiting, ViewProjects, ViewSomeday, ViewLogbook}

// inState is the view predicate of #entries before the sequential project rule.
func inState(e Task, v View) bool {
	switch v {
	case ViewFocus:
		return e.Starred() && e.Open()
	case ViewInbox:
		return e.Category == CategoryInbox
	case ViewNext:
		return e.Category == CategoryReady
	case ViewWaiting:
		return e.Category == CategoryWaiting
	case ViewProjects:
		return e.IsProject() && e.Open()
	case ViewSomeday:
		return inSomeday(e)
	case ViewLogbook:
		return e.Category == CategoryDone
	case ViewOpen:
		return e.Open()
	}
	return false
}

// inSomeday says whether the task is deferred with no snooze to wake it.
func inSomeday(e Task) bool { return e.Category == CategoryDeferred && !e.Snoozed() }

// Matches says whether e passes q's area and contexts; the Inbox ignores the area.
func (q Query) Matches(e Task) bool {
	if q.Area != "" && q.View != ViewInbox && e.Prop(PropArea).Text != q.Area {
		return false
	}
	for _, c := range q.Contexts {
		if !slices.Contains(e.Prop(PropContexts).List, c) {
			return false
		}
	}
	return true
}

// Select is q's view of all, in the view's order: the predicate of #entries, then area and contexts; in Next a
// sequential project shows only its first open action by Position. all must hold every entry the predicate
// may need: the projects and their actions.
func Select(all []Task, q Query) []Task {
	var first map[ID]ID
	if q.View == ViewNext {
		first = firstActions(all)
	}
	var out []Task
	for _, e := range all {
		if !inState(e, q.View) || !q.Matches(e) {
			continue
		}
		if id, ok := first[e.Parent]; ok && id != e.ID {
			continue
		}
		out = append(out, e)
	}
	Sort(out, q.View)
	return out
}

// firstActions maps each sequential project to its open action with the lowest Position.
func firstActions(all []Task) map[ID]ID {
	seq := map[ID]bool{}
	for _, e := range all {
		if e.IsProject() && e.Sequential {
			seq[e.ID] = true
		}
	}
	first := map[ID]Task{}
	for _, e := range all {
		if !seq[e.Parent] || !e.Open() {
			continue
		}
		if f, ok := first[e.Parent]; !ok || cmp.Or(cmp.Compare(e.Position, f.Position), cmp.Compare(e.ID, f.ID)) < 0 {
			first[e.Parent] = e
		}
	}
	out := make(map[ID]ID, len(seq))
	for p := range seq {
		out[p] = first[p].ID
	}
	return out
}

// Sort orders a view's entries: the Logbook the newest first; the others the oldest first.
func Sort(es []Task, v View) {
	slices.SortStableFunc(es, func(a, b Task) int {
		if v == ViewLogbook {
			return cmp.Or(b.Updated.Compare(a.Updated), cmp.Compare(a.ID, b.ID))
		}
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
}

// CountsOf is the counters of the stored views over all, each the length of Select for its view.
func CountsOf(all []Task, area string) Counters {
	c := Counters{Area: area, Views: map[View]int{ViewOutbox: 0}, Areas: map[string]int{}, Contexts: map[string]int{}}
	for _, v := range StoredViews {
		c.Views[v] = len(Select(all, Query{View: v, Area: area}))
	}
	for _, e := range Select(all, Query{View: ViewNext}) {
		a := e.Prop(PropArea).Text
		if a != "" {
			c.Areas[a]++
		}
		if area != "" && a != area {
			continue
		}
		for _, ctx := range e.Prop(PropContexts).List {
			c.Contexts[ctx]++
		}
	}
	c.Badge = c.Views[ViewInbox] + c.Views[ViewOutbox]
	return c
}
