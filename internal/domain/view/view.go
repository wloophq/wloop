// Package view is the item view the loop projects to every sink: a semantic
// status, unread and a summary line, never a sink's vocabulary.
package view

import (
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Status string

const (
	None       Status = ""
	Working    Status = "working"
	NeedsMe    Status = "needs-me"
	Reviewed   Status = "reviewed"
	Done       Status = "done"
	InProgress Status = "in-progress"
	InReview   Status = "in-review"
	Ready      Status = "ready"
	Completed  Status = "completed"
	// Elsewhere is a card the sink does not manage the column of: the user's.
	Elsewhere Status = "elsewhere"
)

type Role string

const (
	ReviewRole Role = "review"
	AuthorRole Role = "author"
)

// Key names an item: a unit's card of one role.
type Key struct {
	Unit unit.Unit
	Role Role
}

type Item struct {
	Unit    unit.Unit
	Role    Role
	Status  Status
	Unread  bool
	Summary string
	// At is the time of the newest turn the item projects; the newest unit
	// owns a card shared by several.
	At time.Time
}

func (it Item) Key() Key { return Key{it.Unit, it.Role} }

// Applied is what a sink last received for an item: the column it wrote,
// or, when the user's placement is outside the sink's managed vocabulary,
// that placement as Override and no column.
type Applied struct {
	Unit     unit.Unit
	Role     Role
	Status   Status
	Unread   bool
	Summary  string
	Column   string
	Override string
	At       time.Time
}

func (a Applied) Key() Key { return Key{a.Unit, a.Role} }

// Review projects the newest turn of intents, those whose board is review;
// a turn of any other intent never moves a review item.
func Review(b turn.Book, intents []turn.Intent) Item {
	it := Item{Unit: b.Unit, Role: ReviewRole}
	t, ok := newest(b, intents)
	if !ok {
		return it
	}
	it.Summary, it.At = summary(&b, t), activeAt(t)
	switch t.State {
	case turn.Sent, turn.Started:
		it.Status = Working
	case turn.Asked, turn.Failed, turn.Unrecorded:
		it.Status, it.Unread = NeedsMe, true
	case turn.Done:
		it.Status = Reviewed
		if r, ok := b.LastRecord(t.Seq); ok && r.Record.Outcome != nil && r.Record.Outcome.Verdict == "APPROVE" {
			it.Status = Done
		}
	case turn.Settled, turn.Dismissed:
		it.Status = Reviewed
	}
	return it
}

// Author projects an author card. A turn of intents that asks or stopped
// needs me; once last, the status a sink last applied, is needs-me, the turn
// moving on or an answer typed into its session puts the item in progress.
// With f, the pull request's facts also move the card (see AuthorColumn),
// unless the turn needs me. Without a turn, only the facts of a pull request
// with an author workspace move it. Without f, or when nothing moves the
// card, it has no opinion.
func Author(b turn.Book, intents []turn.Intent, last Status, f *AuthorFacts) Item {
	it := Item{Unit: b.Unit, Role: AuthorRole}
	t, ok := newest(b, intents)
	if !ok {
		if f != nil && f.AuthorWorkspace {
			it.Status, it.Unread, it.Summary, _ = AuthorColumn(*f, last)
		}
		return it
	}
	it.At = activeAt(t)
	if (t.State == turn.Asked || t.State.Stopped()) && !typedSince(&b, t) {
		it.Status, it.Unread, it.Summary = NeedsMe, true, summary(&b, t)
		return it
	}
	if f != nil {
		if to, unread, reason, _ := AuthorColumn(*f, last); to != None {
			it.Status, it.Unread, it.Summary = to, unread, reason
			return it
		}
	}
	if last == NeedsMe {
		it.Status = InProgress
		if r, ok := b.LastRecord(t.Seq); ok {
			it.Summary = r.Record.Headline()
		}
		return it
	}
	it.Summary = summary(&b, t)
	return it
}

func activeAt(t *turn.Turn) time.Time {
	return slices.MaxFunc([]time.Time{t.SentAt, t.StartedAt, t.EndedAt}, time.Time.Compare)
}

func summary(b *turn.Book, t *turn.Turn) string {
	r, ok := b.LastRecord(t.Seq)
	if !ok {
		return ""
	}
	switch t.State {
	case turn.Asked:
		return "asks: " + r.Record.Headline()
	case turn.Failed, turn.Unrecorded:
		return "stopped: " + r.Record.Headline()
	}
	return r.Record.Headline()
}

func typedSince(b *turn.Book, t *turn.Turn) bool {
	var since time.Time
	if r, ok := b.LastRecord(t.Seq); ok {
		since = r.At
	}
	for _, a := range b.Answers {
		if a.TurnSeq == t.Seq && a.Route == turn.Typed && a.Session == t.Session && a.At.After(since) {
			return true
		}
	}
	return false
}

// HasTurn is true when b has a turn of intents.
func HasTurn(b turn.Book, intents []turn.Intent) bool {
	_, ok := newest(b, intents)
	return ok
}

// newest is the newest turn of intents; none when intents is empty.
func newest(b turn.Book, intents []turn.Intent) (*turn.Turn, bool) {
	if len(intents) == 0 {
		return nil, false
	}
	return b.Newest(intents...)
}
