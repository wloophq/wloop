package task

import (
	"cmp"
	"slices"
)

// ViewOpen selects every entry neither done nor trash. It is not a served view and has no counter.
const ViewOpen View = "open"

// StoredViews are the views whose entries the store holds, in the order counters list them.
var StoredViews = []View{ViewFocus, ViewInbox, ViewNext, ViewWaiting, ViewProjects, ViewSomeday, ViewLogbook}

// inState is the view predicate of #entries before the sequential project rule.
func inState(e Task, v View) bool {
	switch v {
	case ViewFocus:
		return e.Focus.On && e.Live()
	case ViewInbox:
		return e.State == Inbox
	case ViewNext:
		return e.State == Next
	case ViewWaiting:
		return e.State == Waiting
	case ViewProjects:
		return e.Kind == KindProject && e.Live()
	case ViewSomeday:
		return e.State == Someday
	case ViewLogbook:
		return e.State == Reference || e.State == Done
	case ViewOpen:
		return e.Live()
	}
	return false
}

// Matches says whether e passes q's area and contexts; the Inbox ignores the area.
func (q Query) Matches(e Task) bool {
	if q.Area != "" && q.View != ViewInbox && e.Area != q.Area {
		return false
	}
	for _, c := range q.Contexts {
		if !slices.Contains(e.Contexts, c) {
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
		if id, ok := first[e.Project]; ok && id != e.ID {
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
		if e.Kind == KindProject && e.Sequential {
			seq[e.ID] = true
		}
	}
	first := map[ID]Task{}
	for _, e := range all {
		if !seq[e.Project] || !e.Open() {
			continue
		}
		if f, ok := first[e.Project]; !ok || cmp.Or(cmp.Compare(e.Position, f.Position), cmp.Compare(e.ID, f.ID)) < 0 {
			first[e.Project] = e
		}
	}
	out := make(map[ID]ID, len(seq))
	for p := range seq {
		out[p] = first[p].ID
	}
	return out
}

// Sort orders a view's entries: the Logbook by reference first, then the newest; the others the oldest first.
func Sort(es []Task, v View) {
	slices.SortStableFunc(es, func(a, b Task) int {
		if v == ViewLogbook {
			return cmp.Or(cmp.Compare(rank(a), rank(b)), b.Updated.Compare(a.Updated), cmp.Compare(a.ID, b.ID))
		}
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
}

func rank(e Task) int {
	if e.State == Reference {
		return 0
	}
	return 1
}

// CountsOf is the counters of the stored views over all, each the length of Select for its view.
func CountsOf(all []Task, area string) Counters {
	c := Counters{Area: area, Views: map[View]int{ViewOutbox: 0}, Areas: map[string]int{}, Contexts: map[string]int{}}
	for _, v := range StoredViews {
		c.Views[v] = len(Select(all, Query{View: v, Area: area}))
	}
	for _, e := range Select(all, Query{View: ViewNext}) {
		if e.Area != "" {
			c.Areas[e.Area]++
		}
		if area != "" && e.Area != area {
			continue
		}
		for _, ctx := range e.Contexts {
			c.Contexts[ctx]++
		}
	}
	c.Badge = c.Views[ViewInbox] + c.Views[ViewOutbox]
	return c
}
