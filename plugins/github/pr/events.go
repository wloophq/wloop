package pr

import "time"

// Event is an event on a pull request, which the host keeps.
type Event struct {
	PR    Ref
	Kind  string
	Key   string // natural key; "" for singletons
	Actor string
	At    time.Time
	Data  string
}

// The kinds of the events the plugin records; plugin.toml declares the
// timeline's.
const (
	KindOpened               = "opened"
	KindPush                 = "push" // Key = commit oid
	KindReviewRequest        = "review_requested"
	KindReviewRequestRemoved = "review_request_removed"
	KindReview               = "review"  // Key = review id, Data = STATE:headOid
	KindComment              = "comment" // Key = comment id
	KindCheck                = "check"   // Key = headOid:STATE, At = first sighting
	KindReady                = "ready"
	KindDraft                = "draft"  // back to draft; Actor = who converted it
	KindMerged               = "merged" // Actor = the merger, Data = the merge commit oid
	KindClosed               = "closed"
	KindRoundSent            = "round_sent"
	KindRoundHeld            = "round_held"
	KindRoundBlocked         = "round_blocked"
	KindRoundUnblocked       = "round_unblocked" // Data = what cleared it
	KindWouldAddress         = "would_address"
	KindNudged               = "nudged" // Data = who was asked
	KindTracked              = "tracked"
	KindUntracked            = "untracked"
	KindStatus               = "status"     // Data = board column
	KindFollowed             = "followed"   // Key = the time, Data = the reasons, ","-joined
	KindUnfollowed           = "unfollowed" // Key = the time, Data = the reasons it was followed for
)

// ReviewTurnsFollowed bounds how long after its newest review round a pull
// request stays followed for its follow-ups.
const ReviewTurnsFollowed = 30 * 24 * time.Hour

// RequestKey is the key of a review request or of its removal: the reviewer
// and the request's time to the second, so a re-request is its own event.
func RequestKey(reviewer string, at time.Time) string {
	return reviewer + ":" + at.UTC().Format(time.RFC3339)
}

// Followed tells whether a scope reason or a linked workspace holds the pull
// request.
func (p PR) Followed() bool { return len(p.Reasons) > 0 || p.Workspace.ID != "" }
