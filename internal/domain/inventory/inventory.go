// Package inventory holds the records the inventory stores: pull requests,
// their events, the user's overrides, brief records and timing records.
package inventory

import (
	"slices"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

type PR struct {
	Ref            pr.Ref
	Title          string
	Author         string
	State          string // OPEN, MERGED, CLOSED
	IsDraft        bool
	HeadOid        string
	HeadBranch     string
	BaseBranch     string
	IsFork         bool
	ChecksState    string   // SUCCESS, FAILURE, PENDING, "" (no checks)
	ReviewDecision string   // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, "" (no required reviews)
	ReviewRequests []string // current pending reviewers: logins and "team:org/slug"
	URL            string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	MergedAt       time.Time
	ClosedAt       time.Time
	BodyEditedAt   time.Time
	ChangedFiles   int
	LastSeenAt     time.Time // last time a search or a by-ref read returned it
	Reasons        []string  // ownership reasons, sorted; empty means it left scope
	Workspace      Workspace // zero when none
	FailingChecks  []FailingCheck
	Mergeable      string    // MERGEABLE, CONFLICTING, UNKNOWN, "" (not read)
	MergeableAt    time.Time // when it was first seen CONFLICTING; zero otherwise
}

// Followed tells whether a scope reason or a linked workspace holds the pull
// request.
func (p PR) Followed() bool { return len(p.Reasons) > 0 || p.Workspace.ID != "" }

// Lapsed tells an open pull request nothing follows anymore: no sweep
// refreshes it, so no view shows it as live.
// ConflictingSince is when the row was first seen CONFLICTING, and whether
// it still is.
func (p PR) ConflictingSince() (time.Time, bool) {
	return p.MergeableAt, p.Mergeable == "CONFLICTING" && !p.MergeableAt.IsZero()
}

func (p PR) Lapsed() bool { return p.State == dgh.StateOpen && !p.Followed() }

type FailingCheck struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type Workspace struct {
	// Manager owns the workspace; ID is its id there.
	Manager     string            `json:"manager,omitempty"`
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Status      string            `json:"status"`
	IsReview    bool              `json:"is_review"`
	AgentStates []turn.AgentState `json:"agent_states"`
	Comment     string            `json:"comment"`
	// IsMain is the repository's main checkout on the pull request's branch,
	// which the loop never removes.
	IsMain bool `json:"is_main"`
}

type Event struct {
	PR    pr.Ref    `json:"pr"`
	Kind  string    `json:"kind"`
	Key   string    `json:"key"` // natural key; "" for singletons
	Actor string    `json:"actor"`
	At    time.Time `json:"at"`
	Data  string    `json:"data"`
}

// Reviewer is who a KindReviewRequest or KindReviewRequestRemoved event names.
func (e Event) Reviewer() string { return e.Data }

// ActivityEvent is a feed event with the pull request it belongs to.
type ActivityEvent struct {
	Event
	Title, URL, Author, State string
}

// Situation is a pull request's situation as its row derives it,
// "<kind>:<waiting-on>", at the time it was seen.
type Situation struct {
	PR   pr.Ref
	Data string
	At   time.Time
}

// StoredTurn is the text a turn was sent with, kept under its id.
type StoredTurn struct {
	ID, Kind, Repo, Text string
	Sources              []string
	BundledSHA           string
}

const (
	KindOpened = "opened"
	KindPush   = "push" // Key = commit oid
	// KindReviewRequest: Key = reviewer:requestedAt (RFC 3339), Data = the
	// reviewer (login or team:org/slug), Actor = who requested.
	KindReviewRequest = "review_requested"
	// KindReviewRequestRemoved: keyed and shaped as KindReviewRequest.
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
	KindRoundPending         = "round_pending" // Data = "<kind>: <reason>"
	KindRoundSkipped         = "round_skipped" // Data = "<kind>: <why>"
	// KindGuidance: the guidance a round's prompt carried, kept whether or
	// not the prompt landed. Key = when it was sent.
	KindGuidance = "guidance"
	// KindRoundBlocked: a round cannot start until the user frees the PR's
	// head branch, trusts its folder, or answers a question the agent would
	// stop on. Data = why; Key = StartBlock.Key, or "".
	KindRoundBlocked   = "round_blocked"
	KindRoundUnblocked = "round_unblocked" // Data = what cleared it
	KindMuted          = "muted"
	KindUnmuted        = "unmuted"
	KindHeld           = "held"
	KindReleased       = "released"
	KindAdopted        = "adopted"
	KindTracked        = "tracked"
	KindUntracked      = "untracked"
	KindNudged         = "nudged"  // Data = who was asked
	KindRemoved        = "removed" // the local workspace of a finished pull request was removed
	KindStatus         = "status"  // Data = board column
	// KindWouldReview: a follow-up review the sweep observed without running,
	// its review workspace gone. Key = the head, Data = the reason.
	KindWouldReview = "would_review"
	// KindWouldAddress: a triage round on a pull request I develop that the
	// sweep observed without running, the pull request having no author
	// workspace. Key = the fingerprint of the reviews, Data = the reason.
	KindWouldAddress = "would_address"
	// KindReviewEnded: the sweep read a pull request with a review turn and no
	// review workspace closed or merged, and reads it no more. Key = the state.
	KindReviewEnded = "review_ended"
)

const (
	KindFollowed   = "followed"   // Key = the time, Data = the reasons, ","-joined
	KindUnfollowed = "unfollowed" // Key = the time, Data = the reasons it was followed for
	KindSituation  = "situation"  // Key = the pull request's 1-based situation number, Data = "<kind>:<waiting-on>"
)

const (
	KindBriefEnded  = "brief_ended"   // Key = run id, Data = outcome
	KindBriefWanted = "brief_wanted"  // Key = the wanted time
	KindBriefGaveUp = "brief_gave_up" // Key = the give-up time, Data = the reason
)

var bookkeepingKinds = []string{KindFollowed, KindUnfollowed, KindSituation, "turn_usage", KindBriefEnded, KindBriefWanted, KindBriefGaveUp, "reconcile_report", workflow.KindPlanned}

// Bookkeeping says whether kind is an event the loop keeps for timings and
// reports, which no activity feed or timeline shows.
func Bookkeeping(kind string) bool { return slices.Contains(bookkeepingKinds, kind) }

// BookkeepingKinds are the kinds Bookkeeping accepts.
func BookkeepingKinds() []string { return slices.Clone(bookkeepingKinds) }

type Override struct {
	PR                   pr.Ref    `json:"pr"`
	MutedUntil           time.Time `json:"muted_until"`
	MuteUntilStateChange bool      `json:"mute_until_state_change"`
	MutedKind            string    `json:"muted_kind"`       // situation kind when muted until it changes
	MutedWaitingOn       string    `json:"muted_waiting_on"` // and who it was waiting on
	Hold                 bool      `json:"hold"`
	Adopted              bool      `json:"adopted"`
	Tracked              bool      `json:"tracked"`       // added by hand; the store keeps ReasonTracked among its reasons while set
	NextGuidance         string    `json:"next_guidance"` // and guidance for the next one
}

// ReasonTracked is the scope reason of a pull request the user tracks.
const ReasonTracked = "tracked"

// BriefRecord is the brief queue's view of a pull request, assembled from
// pr_briefs, its run (the ok run whose brief is in use) and its last run.
// Brief is the JSON, "" until a run succeeds; CreatedAt is the end of the
// run that made it and AttemptedAt the start of the last attempt.
type BriefRecord struct {
	PR           pr.Ref
	Version      string
	HeadOid      string
	Model        string
	BodyEditedAt time.Time
	ChangedFiles int
	Brief        string
	Error        string
	AttemptedAt  time.Time
	CreatedAt    time.Time
	CostUSD      float64
	// Failures counts the attempts that failed since the last success, or
	// since the pull request last changed.
	Failures int
	// AttemptOf names what the last attempt was made from.
	AttemptOf                   string
	Run, LastRun                int64
	WantedAt, RetryAt, GaveUpAt time.Time
	GaveUpReason                string
	// InputSHA is the input hash of the run in use, empty for a run made without the diff.
	InputSHA string
}

// BriefRun is one brief attempt. A nil duration, cost, token count or diff
// size was not measured, which is not zero.
type BriefRun struct {
	ID                       int64
	PR                       pr.Ref
	Trigger                  string // wanted, background, refresh, trial
	Intake                   int64
	Version, AttemptOf       string
	HeadOid                  string
	BodyEditedAt             time.Time
	ChangedFiles             int
	StartedAt, EndedAt       time.Time
	InputMS, CallMS, AgentMS *int64
	Outcome, Error, Model    string
	CostUSD                  *float64
	Tokens                   *usage.Tokens
	DiffBytes                *int
	DiffCut                  bool
	Brief                    string
	Files, Tags, DroppedTags []string
	// InputSHA hashes what the run read; ReusedFrom is the run whose brief a reused run carries.
	InputSHA   string
	ReusedFrom int64
}

// TimedTurn is a turn's times, as a timings report reads them.
type TimedTurn struct {
	Seq                        int64
	PR                         pr.Ref
	Intent, Trigger, State     string
	Agent                      string
	SentAt, StartedAt, EndedAt time.Time
}

// TurnTokens is one model's usage in one turn; CostUSD is nil when the
// agent did not report it.
type TurnTokens struct {
	TurnSeq                              int64
	Model                                string
	Input, Output, CacheRead, CacheWrite int64
	CostUSD                              *float64
}

// TimedIntake is a user's request's times.
type TimedIntake struct {
	Seq                             int64
	PR                              pr.Ref
	Intent, Source, State, Brief    string
	AcceptedAt, StartingAt, EndedAt time.Time
}

// TagTally counts the pull requests whose current brief carries key:value.
type TagTally struct {
	Key, Value string
	Count      int
}

// TimingsData is the raw record of a window: the turns sent in it with
// their tokens, the intakes accepted in it, the brief runs started in it
// (without their brief and files),
// the breaker's openings, and every pull request with an event in it, with
// all its events.
type TimingsData struct {
	RecordedSince time.Time
	Turns         []TimedTurn
	Tokens        []TurnTokens
	Intakes       []TimedIntake
	Runs          []BriefRun
	Outages       int
	PRs           []PR
	Events        map[pr.Ref][]Event
	Tags          []TagTally
	// PRTags and PRFiles are each pull request's current brief's tags and
	// the files it read.
	PRTags, PRFiles map[pr.Ref][]string
}
