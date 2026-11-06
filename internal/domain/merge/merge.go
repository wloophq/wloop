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
	// From's order after Into's, and in Units also those From took over, as an heir (Split.Heirs), when a task
	// merged into Into before it was split out. Opened are the refs Into held resolved that From held open, which moved into
	// Into's open items. Related are the ids Into's property of role related gained from From's, without
	// From's and Into's own; no third task is written.
	Items, Resolved, Opened []task.ItemRef
	Units                   []unit.Unit
	Related                 []task.ID
	// Gates, Drafts and Messages are the ids of the rows that named From and name Into after the merge.
	Gates, Drafts, Messages []int64
	// Superseded are the open gates the merge superseded.
	Superseded []int64
	// IntoProps are Into's properties of the loop's roles (task.LoopRoles) before the merge, with their writers,
	// so a split that leaves one as it was gives it its writer back.
	IntoProps   task.Props
	IntoWritten map[string]task.Written
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
	for _, role := range task.LoopRoles {
		if name, _ := st.PropOf(role); name != "" {
			if v, ok := into.Props[name]; ok {
				if r.IntoProps == nil {
					r.IntoProps, r.IntoWritten = task.Props{}, map[string]task.Written{}
				}
				r.IntoProps[name], r.IntoWritten[name] = v, into.Written[name]
			}
		}
	}
	merged := append(slices.Clone(into.Prop(mergedProp).List), string(from.ID))
	merged = appendNew(merged, from.Prop(mergedProp).List...)
	setList(&y, mergedProp, merged, now)
	if relatedProp, _ := st.PropOf(task.RoleRelated); relatedProp != "" {
		own := into.Prop(relatedProp).List
		related := slices.DeleteFunc(slices.Clone(own), func(id string) bool { return id == string(from.ID) })
		for _, id := range from.Prop(relatedProp).List {
			if id != string(from.ID) && id != string(into.ID) && !slices.Contains(related, id) {
				related = append(related, id)
				r.Related = append(r.Related, task.ID(id))
			}
		}
		setList(&y, relatedProp, related, now)
	}
	y.Updated = now
	return y, r, nil
}

// setList sets e's property name to list as the loop writes it; a list it holds already keeps its writer.
func setList(e *task.Task, name string, list []string, at time.Time) {
	if slices.Equal(e.Prop(name).List, list) {
		return
	}
	e.SetProp(name, task.Value{List: list}, task.WriterWorkflow, at)
}

// Project is the project a merged task's parent names, as stored when the task is given back: Task is zero
// when no such task is stored, and Children are its children but the merged task.
type Project struct {
	Task     task.Task
	Children []task.Task
}

// Split is what a split writes: From, the task given back, and Rest, the survivor without it. From takes back
// every gate, held draft and message of Units, the units of r.Units no other task still merged into Rest holds,
// and those of r's ids; Heirs are the records of the tasks still merged into Rest that take over each other unit
// of r.Units, with it added to their Units, and Others every record of a task still merged into Rest, whose
// rows stay.
type Split struct {
	From, Rest    task.Task
	Units         []unit.Unit
	Heirs, Others []Record
}

// Unfold gives r.From back from into, as the split of it writes it. others are the records of the other tasks
// merged into into, and owners the task each ref into holds derives (task.DerivedID of its unit and task key),
// for the refs whose item the ledger still has.
//
// From takes the refs of r.Task into holds now, on the side it holds them (a ref into resolved comes back
// resolved), and every other ref that derives From or a task merged into it, or that derives none of the
// others and lies on a unit of Units; into keeps the rest, so what arrived for itself,
// or for another task merged into it, stays. A ref goes to one task only; one both held before the merge
// stays on both. From takes everything else from r.Task: state, title, notes, properties, wake; its parent
// stays only while p, that project, is open, and its position is after its siblings' when another took it. peers
// are the lists of role related of the tasks r.Task lists there and of r.Related, by the id listed, as each
// reads now: From keeps a task of its list only while that one still lists From, and Rest keeps an id of
// r.Related only when that task lists Into by Into's own id; an id peers lacks stays on From and leaves Rest.
// Rest loses r.Opened back to its resolved refs, the units of Units, the ids of r.Related it does not keep, and
// From's ids off its property of role merged; a property of the loop's roles that ends as it was before the
// merge gets its writer back. The
// caller then syncs From's units, so From equals r.Task as that sync leaves it, and r.Task exactly when nothing
// touched From's units while merged.
func Unfold(into task.Task, r Record, others []Record, owners map[task.ItemRef]task.ID, p Project, peers map[task.ID][]string, st task.Profile, now time.Time) (Split, error) {
	if into.ID != r.Into {
		return Split{}, fmt.Errorf("task %s was merged into %s, not %s", r.From, r.Into, into.ID)
	}
	mergedProp, _ := st.PropOf(task.RoleMerged)
	if mergedProp == "" || !slices.Contains(into.Prop(mergedProp).List, string(r.From)) {
		return Split{}, fmt.Errorf("task %s does not list %s as merged into it", into.ID, r.From)
	}
	out := Split{Others: others}
	mine := append([]string{string(r.From)}, r.Task.Prop(mergedProp).List...)
	theirs := []string{string(into.ID)}
	for _, o := range others {
		theirs = append(theirs, string(o.From))
	}
	var heirs []Record
	for _, u := range r.Units {
		i := slices.IndexFunc(others, func(o Record) bool { return slices.Contains(o.Task.Units, u) })
		if i < 0 {
			out.Units = append(out.Units, u)
			continue
		}
		h := slices.IndexFunc(heirs, func(o Record) bool { return o.From == others[i].From })
		if h < 0 {
			heirs, h = append(heirs, others[i]), len(heirs)
			heirs[h].Units = slices.Clone(heirs[h].Units)
		}
		heirs[h].Units = append(heirs[h].Units, u)
	}
	out.Heirs = heirs
	was := func(ref task.ItemRef) bool {
		return slices.Contains(r.Task.Items, ref) || slices.Contains(r.Task.Resolved, ref)
	}
	takes := func(ref task.ItemRef) bool {
		if was(ref) {
			return true
		}
		if owner, ok := owners[ref]; ok && slices.Contains(mine, string(owner)) {
			return true
		} else if ok && slices.Contains(theirs, string(owner)) {
			return false
		}
		return slices.Contains(out.Units, ref.Unit)
	}
	shared := func(ref task.ItemRef) bool {
		return was(ref) && !slices.Contains(r.Items, ref) && !slices.Contains(r.Resolved, ref) && !slices.Contains(r.Opened, ref)
	}
	from := r.Task
	from.Items, from.Resolved = nil, nil
	rest := into
	rest.Items, rest.Resolved = nil, nil
	for _, side := range []struct {
		refs           []task.ItemRef
		toFrom, toRest *[]task.ItemRef
	}{{into.Items, &from.Items, &rest.Items}, {into.Resolved, &from.Resolved, &rest.Resolved}} {
		for _, ref := range side.refs {
			if !takes(ref) {
				*side.toRest = append(*side.toRest, ref)
				continue
			}
			*side.toFrom = append(*side.toFrom, ref)
			if shared(ref) {
				*side.toRest = append(*side.toRest, ref)
			}
		}
	}
	for _, ref := range r.Opened {
		if (slices.Contains(into.Items, ref) || slices.Contains(into.Resolved, ref)) && !slices.Contains(rest.Resolved, ref) {
			rest.Resolved = append(rest.Resolved, ref)
		}
	}
	for _, e := range []*task.Task{&from, &rest} {
		sortRefs(e.Items)
		sortRefs(e.Resolved)
		e.Items, e.Resolved = orNil(e.Items), orNil(e.Resolved)
	}
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
	rest.Units = slices.DeleteFunc(slices.Clone(into.Units), func(u unit.Unit) bool { return slices.Contains(out.Units, u) })
	if len(rest.Units) == 0 {
		rest.Units = nil
	}
	merged := slices.DeleteFunc(slices.Clone(into.Prop(mergedProp).List), func(id string) bool { return slices.Contains(mine, id) })
	setBack(&rest, r, mergedProp, merged, now)
	if relatedProp, _ := st.PropOf(task.RoleRelated); relatedProp != "" {
		lists := func(id string, who task.ID) (listed, known bool) {
			l, known := peers[task.ID(id)]
			return slices.Contains(l, string(who)), known
		}
		own := r.Task.Prop(relatedProp).List
		kept := slices.DeleteFunc(slices.Clone(own), func(id string) bool {
			listed, known := lists(id, r.From)
			return id != string(into.ID) && known && !listed
		})
		if !slices.Equal(kept, own) {
			setList(&from, relatedProp, orNilIDs(kept), now)
			from.Updated = now
		}
		related := slices.DeleteFunc(slices.Clone(into.Prop(relatedProp).List), func(id string) bool {
			listed, _ := lists(id, into.ID)
			return slices.Contains(r.Related, task.ID(id)) && !listed
		})
		if slices.Contains(own, string(into.ID)) {
			related = appendNew(related, string(r.From))
		}
		setBack(&rest, r, relatedProp, related, now)
	}
	rest.Updated = now
	out.From, out.Rest = from, rest
	return out, nil
}

// setBack sets rest's property name to list, with the writer it had before r's merge when it holds that list
// again.
func setBack(rest *task.Task, r Record, name string, list []string, at time.Time) {
	if before, ok := r.IntoProps[name]; ok && slices.Equal(before.List, list) {
		w := r.IntoWritten[name]
		rest.SetProp(name, before, w.By, w.At)
		return
	}
	if len(list) == 0 {
		list = nil
	}
	setList(rest, name, list, at)
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

// Pairable says whether t may be the other task of a verb that pairs two: a draft's or a message's task may not,
// as another service serves it. A relation takes a project.
func Pairable(t task.Task) bool { return t.Origin != task.OriginDraft && t.Origin != task.OriginMessage }

// Mergeable says whether t may be merged, or be another's merge candidate: a pairable task that is no project.
func Mergeable(t task.Task) bool { return Pairable(t) && !t.IsProject() }

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

func orNilIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func appendNew(list []string, ids ...string) []string {
	for _, id := range ids {
		if !slices.Contains(list, id) {
			list = append(list, id)
		}
	}
	return list
}
