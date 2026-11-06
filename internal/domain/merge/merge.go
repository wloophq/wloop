// Package merge is a task folded into another and given back: the record a merge keeps and the pure rules of
// both moves.
package merge

import (
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
// its property of role merged gains from's id and its property of role related the ids of Related. The
// store completes Wake, Marks, Gates, Drafts, Messages and Superseded.
func Fold(from, into task.Task, st task.Profile, now time.Time) (task.Task, Record, error) {
	panic("not implemented: T1")
}

// Unfold gives r.From back by unit. Every ref into holds of a unit in r.Units goes back to from, on the side
// into holds it now (a ref into resolved comes back resolved), with the refs r lists of from's other units;
// the rest of into stays, so what arrived on into's own units after the merge is into's. from takes
// everything else from r.Task: state, title, notes, properties, wake; its parent stays only while that
// project is open, and its position is after its siblings' when another took it. into loses r.Opened back
// to its resolved refs, r.Related, and from's id off its property of role merged. The store gives from the
// gates, held drafts and messages of r's units and ids; the caller then syncs from's units, so from equals
// r.Task as that sync leaves it, and r.Task exactly when nothing touched from's units while merged.
func Unfold(into task.Task, r Record, st task.Profile, now time.Time) (from, rest task.Task, err error) {
	panic("not implemented: T1")
}

// Survivor is which of a and b a merge keeps and which it folds: the one aHolds or bHolds alone says
// survives, else the older by Created, then by id.
func Survivor(a, b task.Task, aHolds, bHolds bool) (keep, fold task.Task) {
	panic("not implemented: T1")
}
