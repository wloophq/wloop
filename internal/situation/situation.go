// Package situation names the situations of a pull request's row, those the
// GitHub plugin derives and those the host's overlays set, as the cockpit
// ranks and words them.
package situation

type Kind string

const (
	KindDone         Kind = "done"
	KindMuted        Kind = "muted"
	KindHeldByCap    Kind = "held"
	KindBlocked      Kind = "blocked"
	KindToAddress    Kind = "to_address"
	KindRoundPending Kind = "round_pending"
	KindLoopWorking  Kind = "loop_working"
	KindLoopWaiting  Kind = "loop_waiting"
	KindLoopStopped  Kind = "loop_stopped"
	KindDraft        Kind = "draft"
	KindChecks       Kind = "checks"
	KindChanges      Kind = "changes"
	KindMerge        Kind = "merge"
	KindConflicts    Kind = "conflicts"
	KindReply        Kind = "reply"
	KindReviewerBack Kind = "reviewer_back"
	KindReview       Kind = "review"
	KindNoReviewer   Kind = "no_reviewer"
)

var labels = map[Kind]string{
	KindDone: "closed", KindMuted: "muted", KindHeldByCap: "held (cap)", KindBlocked: "round blocked", KindToAddress: "to address",
	KindRoundPending: "round soon", KindLoopWorking: "agent working", KindLoopWaiting: "agent asks", KindLoopStopped: "agent stopped", KindDraft: "draft",
	KindChecks: "checks failing", KindChanges: "changes asked", KindMerge: "can merge", KindConflicts: "merge conflicts", KindReply: "reply due",
	KindReviewerBack: "reviewer due", KindReview: "review", KindNoReviewer: "no reviewer",
}

// Order is every kind as the cockpit ranks them: what needs the user first.
var Order = []Kind{KindRoundPending, KindLoopWaiting, KindLoopStopped, KindBlocked, KindToAddress, KindHeldByCap, KindLoopWorking,
	KindChanges, KindChecks, KindConflicts, KindReply, KindReviewerBack, KindReview, KindNoReviewer, KindMerge, KindDraft, KindMuted, KindDone}

// Label is the kind in a few words, as the cockpit and the TUI show it.
func (k Kind) Label() string {
	if l, ok := labels[k]; ok {
		return l
	}
	return string(k)
}
