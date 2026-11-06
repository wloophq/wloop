// Package merge is a task folded into another and given back: the record a merge keeps and the pure rules of
// both moves.
package merge

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

// RecordKind is the kind of the event that keeps a merge's Record, as JSON, on the merged task's own unit
// (task.UnitOf(From)), whose events retention keeps forever. While From is not stored, the record of its
// merge is the one of these events with the highest event id.
const RecordKind = "merge_record"

// Record is task From folded into task Into: what the merge moved, and enough for a split to give From back.
//
// From's own state goes with its row: Task, Wake and Marks hold it. The rows that act for From follow it to
// Into, by id: Gates, Drafts and Messages. The open gates that would act on Into in From's name, the
// proposals, plans and steps on From's own unit and the proposals whose subject names From, are superseded,
// listed in Superseded, and never reopened. Every other row that names From (its events, chat and
// timeline, turns, sessions, workflow runs and plan items, classifications, links, dossier) stays under
// From's id, and Into's readers reach it through the ids merged into Into.
type Record struct {
	From, Into task.ID
	At         time.Time
	// Task is From as stored the moment before the merge, with its properties and their writers, its items,
	// resolved items, units in order, parent and position.
	Task task.Task
	// Wake is From's wake then, nil when it held none.
	Wake *wake.Wake
	// Marks are the marks From's own unit held then, by name; retention drops the marks of a task no longer
	// stored, so a split writes these back.
	Marks map[string]string
	// Items, Resolved and Units are what Into gained: From's item refs and units Into did not hold, units in
	// From's order after Into's. Opened are the refs Into held resolved that From held open, which moved into
	// Into's open items. Related are the ids Into's property of role related gained from From's, without
	// From's and Into's own; no third task is written.
	Items, Resolved, Opened []task.ItemRef
	Units                   []unit.Unit
	Related                 []task.ID
	// Gates, Drafts and Messages are the ids of the rows that named From and name Into after the merge.
	Gates, Drafts, Messages []int64
	// Superseded are the open gates the merge superseded.
	Superseded []int64
}

// Fold is into once from is merged into it, at now, and the record of the merge as far as the two tasks
// tell it: From, Into, At, Task and what into gained. into keeps its state, properties, parent and title;
// its property of role merged gains from's id and the ids from's lists, and its property of role related the
// ids of Related. The store completes Wake, Marks, Gates, Drafts, Messages and Superseded.
func Fold(from, into task.Task, st task.Profile, now time.Time) (task.Task, Record, error) {
	if from.ID == into.ID {
		return into, Record{}, fmt.Errorf("task %s cannot be merged into itself", from.ID)
	}
	mergedProp, _ := st.PropOf(task.RoleMerged)
	if mergedProp == "" {
		return into, Record{}, errors.New("no property takes the role merged, so no merge can be given back")
	}
	r := Record{From: from.ID, Into: into.ID, At: now, Task: from}
	y := into
	y.Items, y.Resolved, y.Units = slices.Clone(into.Items), slices.Clone(into.Resolved), slices.Clone(into.Units)
	for _, ref := range from.Items {
		switch {
		case slices.Contains(y.Items, ref):
		case slices.Contains(y.Resolved, ref):
			y.Resolved = slices.DeleteFunc(y.Resolved, func(o task.ItemRef) bool { return o == ref })
			y.Items = append(y.Items, ref)
			r.Opened = append(r.Opened, ref)
		default:
			y.Items = append(y.Items, ref)
			r.Items = append(r.Items, ref)
		}
	}
	for _, ref := range from.Resolved {
		if !slices.Contains(y.Items, ref) && !slices.Contains(y.Resolved, ref) {
			y.Resolved = append(y.Resolved, ref)
			r.Resolved = append(r.Resolved, ref)
		}
	}
	for _, u := range from.Units {
		if !slices.Contains(y.Units, u) {
			y.Units = append(y.Units, u)
			r.Units = append(r.Units, u)
		}
	}
	sortRefs(y.Items)
	sortRefs(y.Resolved)
	y.Items, y.Resolved = orNil(y.Items), orNil(y.Resolved)
	merged := append(slices.Clone(into.Prop(mergedProp).List), string(from.ID))
	merged = appendNew(merged, from.Prop(mergedProp).List...)
	y.SetProp(mergedProp, task.Value{List: merged}, task.WriterWorkflow, now)
	if relatedProp, _ := st.PropOf(task.RoleRelated); relatedProp != "" {
		own := into.Prop(relatedProp).List
		related := slices.DeleteFunc(slices.Clone(own), func(id string) bool { return id == string(from.ID) })
		for _, id := range from.Prop(relatedProp).List {
			if id != string(from.ID) && id != string(into.ID) && !slices.Contains(related, id) {
				related = append(related, id)
				r.Related = append(r.Related, task.ID(id))
			}
		}
		y.SetProp(relatedProp, task.Value{List: related}, task.WriterWorkflow, now)
	}
	y.Updated = now
	return y, r, nil
}

// Project is the project a merged task's parent names, as stored when the task is given back: Task is zero
// when no such task is stored, and Children are its children but the merged task.
type Project struct {
	Task     task.Task
	Children []task.Task
}

// Unfold gives r.From back by unit. Every ref into holds of a unit in r.Units goes back to from, on the side
// into holds it now (a ref into resolved comes back resolved), with the refs of r.Task's other units that into
// still holds; those into gained from the merge leave it. The rest of into stays, so what arrived on into's
// own units after the merge is into's. from takes everything else from r.Task: state, title, notes,
// properties, wake; its parent stays only while p, that project, is open, and its position is after its
// siblings' when another took it. into loses r.Opened back to its resolved refs, r.Related, and from's ids off
// its property of role merged. The store gives from the gates, held drafts and messages of r's units and ids;
// the caller then syncs from's units, so from equals r.Task as that sync leaves it, and r.Task exactly when
// nothing touched from's units while merged.
func Unfold(into task.Task, r Record, p Project, st task.Profile, now time.Time) (from, rest task.Task, err error) {
	if into.ID != r.Into {
		return task.Task{}, task.Task{}, fmt.Errorf("task %s was merged into %s, not %s", r.From, r.Into, into.ID)
	}
	mergedProp, _ := st.PropOf(task.RoleMerged)
	if mergedProp == "" || !slices.Contains(into.Prop(mergedProp).List, string(r.From)) {
		return task.Task{}, task.Task{}, fmt.Errorf("task %s does not list %s as merged into it", into.ID, r.From)
	}
	brought := func(ref task.ItemRef) bool { return slices.Contains(r.Units, ref.Unit) }
	was := func(ref task.ItemRef) bool {
		return slices.Contains(r.Task.Items, ref) || slices.Contains(r.Task.Resolved, ref)
	}
	gained := func(ref task.ItemRef) bool {
		return brought(ref) || slices.Contains(r.Items, ref) || slices.Contains(r.Resolved, ref)
	}
	from = r.Task
	from.Items, from.Resolved = nil, nil
	for _, ref := range into.Items {
		if brought(ref) || was(ref) {
			from.Items = append(from.Items, ref)
		}
	}
	for _, ref := range into.Resolved {
		if brought(ref) || was(ref) {
			from.Resolved = append(from.Resolved, ref)
		}
	}
	sortRefs(from.Items)
	sortRefs(from.Resolved)
	from.Items, from.Resolved = orNil(from.Items), orNil(from.Resolved)
	if from.Parent != "" {
		switch {
		case p.Task.ID != from.Parent || !p.Task.Open():
			from.Parent, from.Position = "", 0
		case slices.ContainsFunc(p.Children, func(c task.Task) bool { return c.ID != from.ID && c.Position == from.Position }):
			last := 0
			for _, c := range p.Children {
				if c.ID != from.ID {
					last = max(last, c.Position)
				}
			}
			from.Position = last + 1
		}
	}
	if !from.Same(r.Task) {
		from.Updated = now
	}

	rest = into
	rest.Items = slices.DeleteFunc(slices.Clone(into.Items), func(ref task.ItemRef) bool { return gained(ref) || slices.Contains(r.Opened, ref) })
	rest.Resolved = slices.DeleteFunc(slices.Clone(into.Resolved), gained)
	for _, ref := range r.Opened {
		if (slices.Contains(into.Items, ref) || slices.Contains(into.Resolved, ref)) && !slices.Contains(rest.Resolved, ref) {
			rest.Resolved = append(rest.Resolved, ref)
		}
	}
	sortRefs(rest.Items)
	sortRefs(rest.Resolved)
	rest.Items, rest.Resolved = orNil(rest.Items), orNil(rest.Resolved)
	rest.Units = slices.DeleteFunc(slices.Clone(into.Units), func(u unit.Unit) bool { return slices.Contains(r.Units, u) })
	if len(rest.Units) == 0 {
		rest.Units = nil
	}
	gone := append([]string{string(r.From)}, r.Task.Prop(mergedProp).List...)
	merged := slices.DeleteFunc(slices.Clone(into.Prop(mergedProp).List), func(id string) bool { return slices.Contains(gone, id) })
	rest.SetProp(mergedProp, task.Value{List: merged}, task.WriterWorkflow, now)
	if relatedProp, _ := st.PropOf(task.RoleRelated); relatedProp != "" {
		related := slices.DeleteFunc(slices.Clone(into.Prop(relatedProp).List), func(id string) bool { return slices.Contains(r.Related, task.ID(id)) })
		if slices.Contains(r.Task.Prop(relatedProp).List, string(into.ID)) {
			related = appendNew(related, string(r.From))
		}
		rest.SetProp(relatedProp, task.Value{List: related}, task.WriterWorkflow, now)
	}
	rest.Updated = now
	return from, rest, nil
}

// Survivor is which of a and b a merge keeps and which it folds: the one aHolds or bHolds alone says
// survives, else the older by Created, then by id.
func Survivor(a, b task.Task, aHolds, bHolds bool) (keep, fold task.Task) {
	if aHolds != bHolds {
		if aHolds {
			return a, b
		}
		return b, a
	}
	if cmp.Or(a.Created.Compare(b.Created), strings.Compare(string(a.ID), string(b.ID))) <= 0 {
		return a, b
	}
	return b, a
}

func sortRefs(rs []task.ItemRef) {
	slices.SortFunc(rs, func(a, b task.ItemRef) int {
		return cmp.Or(cmp.Compare(a.Unit.String(), b.Unit.String()), strings.Compare(a.Item, b.Item))
	})
}

func orNil(rs []task.ItemRef) []task.ItemRef {
	if len(rs) == 0 {
		return nil
	}
	return rs
}

func appendNew(list []string, ids ...string) []string {
	for _, id := range ids {
		if !slices.Contains(list, id) {
			list = append(list, id)
		}
	}
	return list
}
