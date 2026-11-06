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
// (task.UnitOf(From)), whose events are kept forever. The latest one is the merge that took From while From
// is not stored.
const RecordKind = "merge_record"

// Record is task From folded into task Into: what the merge moved, and enough for a split to give From back
// exactly as it was while Into keeps whatever reached it after the merge.
//
// From's own state goes with its row: Task, Wake and Marks hold it. The rows that act for From follow it to
// Into, by id: Gates, Drafts and Messages. Every other row that names From (its events, chat and timeline,
// turns, sessions, workflow runs and plan items, classifications, links, dossier) stays under From's id,
// which the alias From to Into resolves, and is From's again once split.
type Record struct {
	From, Into task.ID
	At         time.Time
	// Task is From as stored the moment before the merge, with its properties and their writers, its items,
	// resolved items and units in order; a split writes it back as it is.
	Task task.Task
	// Wake is From's wake then, nil when it held none.
	Wake *wake.Wake
	// Marks are the marks From's own unit held then, by name; retention drops the marks of a task no longer
	// stored, so a split writes these back.
	Marks map[string]string
	// Items, Resolved and Units are what Into gained: From's item refs and units Into did not hold, units in
	// From's order after Into's. Opened are the refs Into held resolved that From held open, which moved into
	// Into's open items. A split takes exactly these off Into, Opened back to its resolved ones.
	Items, Resolved, Opened []task.ItemRef
	Units                   []unit.Unit
	// Gates, Drafts and Messages are the ids of the rows that named From and name Into after the merge; a
	// split points them back at From.
	Gates, Drafts, Messages []int64
}

// Fold is into once from is merged into it, at now, and the record of the merge as far as the tasks tell it:
// From, Into, At, Task and what into gained. into keeps its state, properties, parent and title; the property
// of role merged gains from's id. The store completes Wake, Marks, Gates, Drafts and Messages.
func Fold(from, into task.Task, st task.Profile, now time.Time) (task.Task, Record, error) {
	panic("not implemented: T1")
}

// Unfold is from as r records it and into without what r says it gained, its Opened refs resolved again and
// from's id off its property of role merged; whatever else into holds stays.
func Unfold(into task.Task, r Record, st task.Profile, now time.Time) (from, rest task.Task, err error) {
	panic("not implemented: T1")
}

// Survivor is which of a and b a merge keeps and which it folds: the one aHolds or bHolds alone says
// survives, else the older by Created, then by id.
func Survivor(a, b task.Task, aHolds, bHolds bool) (keep, fold task.Task) {
	panic("not implemented: T1")
}
