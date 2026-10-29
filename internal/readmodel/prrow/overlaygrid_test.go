package prrow

import (
	"fmt"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/situation"
)

// The grid's pull request, the instant its hours count from, a Monday, and
// the stalled thresholds of its sources.
var (
	gridRef = pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	gridT0  = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
)

func h(n int) time.Time { return gridT0.Add(time.Duration(n) * time.Hour) }

func ev(kind, key, actor string, at time.Time, data string) dinv.Event {
	return dinv.Event{PR: gridRef, Kind: kind, Key: key, Actor: actor, At: at, Data: data}
}

func requested(reviewer, actor string, at time.Time) dinv.Event {
	return ev(dinv.KindReviewRequest, reviewer+":"+at.UTC().Format(time.RFC3339), actor, at, reviewer)
}

func gridPR(author string, requested ...string) dinv.PR {
	return dinv.PR{Ref: gridRef, Author: author, State: "OPEN", HeadOid: "h2", ChecksState: "SUCCESS", ReviewRequests: requested}
}

// gridSource is a pull request whose own situation, with no turn and no
// mute, is one of the kinds the overlays cross.
type gridSource struct {
	name string
	pr   dinv.PR
	evs  []dinv.Event
	now  time.Time
}

func gridSources() []gridSource {
	push := ev(dinv.KindPush, "h2", "ana", h(1), "")
	draft, checks, approved, conflicts := gridPR("ana"), gridPR("ana"), gridPR("ana"), gridPR("ana")
	draft.IsDraft = true
	checks.ChecksState = "FAILURE"
	approved.ReviewDecision = "APPROVED"
	conflicts.ReviewDecision, conflicts.Mergeable, conflicts.BaseBranch = "APPROVED", "CONFLICTING", "main"
	merged := gridPR("ana")
	merged.State = "MERGED"
	return []gridSource{
		{"done", merged, []dinv.Event{ev(dinv.KindMerged, "", "ana", h(9), "")}, h(10)},
		{"draft", draft, []dinv.Event{push}, h(10)},
		{"checks", checks, []dinv.Event{push}, h(10)},
		{"changes", gridPR("ana"), []dinv.Event{push, ev(dinv.KindReview, "r1", "me", h(3), "CHANGES_REQUESTED:h2")}, h(10)},
		{"merge", approved, []dinv.Event{push, ev(dinv.KindReview, "r1", "me", h(3), "APPROVED:h2")}, h(10)},
		{"conflicts", conflicts, []dinv.Event{push, ev(dinv.KindReview, "r1", "me", h(3), "APPROVED:h2")}, h(10)},
		{"reply", gridPR("ana", "me"), []dinv.Event{push, requested("me", "ana", h(2)), ev(dinv.KindReview, "r1", "me", h(3), "COMMENTED:h2")}, h(10)},
		{"reviewer_back", gridPR("ana", "me"), []dinv.Event{push, requested("me", "ana", h(2)), ev(dinv.KindReview, "r1", "me", h(3), "COMMENTED:h2"),
			ev(dinv.KindComment, "c1", "ana", h(5), "")}, h(10)},
		{"review", gridPR("ana", "carla"), []dinv.Event{push, requested("carla", "ana", h(2))}, h(10)},
		{"review stalled", gridPR("ana", "me"), []dinv.Event{push, requested("me", "ana", h(2))}, h(100)},
		{"no_reviewer", gridPR("ana"), []dinv.Event{push}, h(10)},
		{"blocked", gridPR("ana", "me"), []dinv.Event{requested("me", "ana", h(1)), ev(dinv.KindRoundBlocked, "", "me", h(9), "the branch is busy")}, h(10)},
		{"held", gridPR("ana", "me"), []dinv.Event{requested("me", "ana", h(1)), ev(dinv.KindRoundHeld, "", "me", h(9), "5 rounds today")}, h(10)},
		{"to_address", gridPR("me", "bob"), []dinv.Event{ev(dinv.KindReview, "r1", "bob", h(2), "CHANGES_REQUESTED:h2"),
			ev(dinv.KindWouldAddress, "w1", "", h(3), "reviewed by bob")}, h(4)},
	}
}

// gridTurn is a turn the run overlay reads.
type gridTurn struct {
	role, state, summary string
	answered             bool
}

func gridTurns() []*gridTurn {
	out := []*gridTurn{nil}
	for _, role := range []string{builtin.RoleReview, builtin.RoleAddress, "pr.nudge"} {
		out = append(out, &gridTurn{role: role, state: "asked", summary: "which branch?"},
			&gridTurn{role: role, state: "asked", summary: "which branch?", answered: true},
			&gridTurn{role: role, state: "failed", summary: "gh api 502"}, &gridTurn{role: role, state: "unrecorded"},
			&gridTurn{role: role, state: "unrecorded", summary: "drop it?"}, &gridTurn{role: role, state: "started"},
			&gridTurn{role: role, state: "ended", summary: "pushed"})
	}
	return out
}

// gridMute is a mute of the grid, with the situation the golden's mute
// until the state changes was set on.
type gridMute struct {
	mute            overlay.Mute
	kind, waitingOn string
}

func gridMutes() []gridMute {
	change := overlay.Mute{UntilStateChange: true}
	return []gridMute{{}, {mute: overlay.Mute{Until: h(200)}}, {mute: overlay.Mute{Until: h(1)}},
		{change, string(situation.KindLoopWaiting), "me"},
		{change, string(situation.KindReview), "carla"},
		{change, string(situation.KindNoReviewer), "author"}}
}

func gridPendings() []*round.Pending {
	return []*round.Pending{nil, {Intent: "follow-up", Reason: "new commits", Since: h(1), Deadline: h(300)}}
}

func (t *gridTurn) String() string {
	if t == nil {
		return "no turn"
	}
	return fmt.Sprintf("%s %s %q answered=%t", t.role, t.state, t.summary, t.answered)
}

func gridCase(s gridSource, t *gridTurn, waiting bool, o gridMute, p *round.Pending) string {
	pend := "no round"
	if p != nil {
		pend = "round " + p.Intent
	}
	until := "never"
	if !o.mute.Until.IsZero() {
		until = o.mute.Until.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s | %s | waiting=%t | mute until=%s change=%t %s/%s | %s", s.name, t, waiting,
		until, o.mute.UntilStateChange, o.kind, o.waitingOn, pend)
}

func gridResult(kind, waitingOn string, since time.Time, reason string, stalled bool) string {
	return fmt.Sprintf("%s %q since=%s %q stalled=%t", kind, waitingOn, since.UTC().Format(time.RFC3339), reason, stalled)
}
