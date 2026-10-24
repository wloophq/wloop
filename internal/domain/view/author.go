package view

import "time"

// The states of AuthorFacts.State.
const (
	StateMerged = "MERGED"
	StateClosed = "CLOSED"
)

// AuthorFacts are what the author column of a pull request is decided from.
type AuthorFacts struct {
	State                                               string
	Draft, Approved, ChecksPass, ChangesRequestedOnHead bool
	Mergeable, Base                                     string
	// ConflictingFor is how long the pull request has been seen CONFLICTING,
	// zero when it has not or without an inventory.
	ConflictingFor, ConfirmConflictAfter time.Duration
	// Local is nil when the checkout was not inspected.
	Local *LocalFacts
	// AuthorWorkspace and ReviewWorkspace say the user's board has a
	// workspace of that role for the pull request, whose card the facts move
	// without a turn; AuthorWorkspace is false while an address round is due,
	// since that round's turn moves the card.
	AuthorWorkspace, ReviewWorkspace bool
}

type LocalFacts struct {
	Clean        bool
	Head, PRHead string
	AheadOfPR    bool
	AgentWorking bool
}

// LocalNeed is how much of the checkout AuthorColumn must read to decide.
type LocalNeed int

const (
	NoLocal LocalNeed = iota
	// LocalHead reads only Head and AheadOfPR.
	LocalHead
	// LocalClean also reads Clean and AgentWorking.
	LocalClean
)

// Finished is true for a merged or closed pull request.
func (f AuthorFacts) Finished() bool { return f.State == StateMerged || f.State == StateClosed }

// AuthorColumn decides where an author card in current moves. to is None when
// it stays; need asks the caller to inspect that much of the checkout and
// decide again with Local set.
func AuthorColumn(f AuthorFacts, current Status) (to Status, unread bool, reason string, need LocalNeed) {
	switch f.State {
	case StateMerged:
		if current == Completed {
			return None, false, "merged, already completed", NoLocal
		}
		return Completed, false, "merged", NoLocal
	case StateClosed:
		if current == Completed {
			return None, false, "closed, already completed", NoLocal
		}
		return Completed, false, "closed without merge", NoLocal
	}

	// Only while the author has not pushed since the request: afterwards the
	// agent Stop hook owns in-review, and moving it back would loop every
	// sweep until the reviewer acts.
	if f.ChangesRequestedOnHead && (current == InReview || current == Ready) {
		return InProgress, true, "changes requested", NoLocal
	}

	// UNKNOWN is GitHub still computing it: only a known conflict counts.
	// Leaving ready takes a card the user may have placed by hand, so the
	// conflict must outlive a window: GitHub can answer a transient conflict
	// around a push to the base, and the inventory's read may be seconds old.
	if f.Mergeable == "CONFLICTING" {
		reason = "merge conflicts with " + f.Base
		if current == Ready {
			if f.ConflictingFor <= 0 || f.ConflictingFor < f.ConfirmConflictAfter {
				return None, false, reason + ", not yet confirmed by a second read", NoLocal
			}
			return InProgress, true, reason, NoLocal
		}
		return None, false, reason, NoLocal
	}

	readyCandidate := !f.Draft && f.Approved && f.ChecksPass && (current == InProgress || current == InReview)
	resumeCandidate := current == Ready
	if !readyCandidate && !resumeCandidate {
		return None, false, "nothing to change", NoLocal
	}
	l := f.Local
	if l == nil {
		if resumeCandidate {
			return None, false, "", LocalHead
		}
		return None, false, "", LocalClean
	}
	if resumeCandidate {
		if l.AheadOfPR {
			return InProgress, false, "local commits not pushed yet", NoLocal
		}
		return None, false, "ready, nothing to change", NoLocal
	}
	switch {
	case f.Mergeable == "UNKNOWN":
		return None, false, "approved, but GitHub is still computing whether it merges cleanly", NoLocal
	case l.AgentWorking:
		return None, false, "approved, but an agent is working", NoLocal
	case !l.Clean:
		return None, false, "approved, but the tree has uncommitted changes", NoLocal
	case l.Head != l.PRHead:
		return None, false, "approved, but local HEAD differs from the PR head", NoLocal
	}
	return Ready, false, "approved and checks pass", NoLocal
}
