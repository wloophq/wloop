// Package situation derives who a pull request is waiting on, and since
// when, from the pull request and its events alone. The host lays its own
// state over it: a run on the pull request, the user's mute and a pending
// round.
package situation

import (
	"fmt"
	"slices"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/workday"
)

// Kind is a situation of plugin.toml's row_kinds.
type Kind string

// The situations the plugin derives.
const (
	KindDone         Kind = "done"
	KindHeldByCap    Kind = "held"
	KindBlocked      Kind = "blocked"
	KindToAddress    Kind = "to_address"
	KindLoopWorking  Kind = "loop_working"
	KindLoopWaiting  Kind = "loop_waiting"
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

// The situations only the host's overlays set, which the plugin's rows,
// cards and items read back.
const (
	KindMuted        Kind = "muted"
	KindRoundPending Kind = "round_pending"
	KindLoopStopped  Kind = "loop_stopped"
)

// Thresholds are how long, in working days, a situation lasts before it is
// stalled; zero never stalls.
type Thresholds struct {
	Review, Reply, Merge time.Duration
}

type Input struct {
	PR      pr.PR
	Events  []pr.Event
	Viewer  string   // my login
	MyTeams []string // "org/slug", any case
	Ignore  []string // actors whose comments and reviews never count: automated reviewers
	// Now is when the situation is derived: a round held by the cap holds
	// only on the local day it was held.
	Now     time.Time
	Stalled Thresholds
	// AddressedAt is when the pull request's driving turn, an address turn,
	// reached its state; zero without one.
	AddressedAt time.Time
}

type Result struct {
	Kind      Kind
	WaitingOn string // login(s), "me", "author", "nobody"
	Since     time.Time
	Reason    string
	// Stalled is when the situation stalls: true once its threshold has
	// passed since Since, in working days; zero never stalls.
	Stalled src.Value
}

// Situation is r as the snapshot reports it.
func (r Result) Situation() *src.Situation {
	return &src.Situation{Kind: string(r.Kind), WaitingOn: r.WaitingOn, Since: r.Since, Reason: r.Reason, Stalled: r.Stalled}
}

// StalledAt says whether r is stalled at now.
func (r Result) StalledAt(now time.Time) bool {
	v := r.Stalled
	return v.Kind == src.KindAfter && !v.Time.IsZero() && workday.Between(v.Time, now) > v.Threshold
}

func Derive(in Input) Result {
	evs := slices.Clone(in.Events)
	slices.SortStableFunc(evs, func(a, b pr.Event) int { return a.At.Compare(b.At) })
	last := func(kinds ...string) (pr.Event, bool) {
		for i := len(evs) - 1; i >= 0; i-- {
			if slices.Contains(kinds, evs[i].Kind) {
				return evs[i], true
			}
		}
		return pr.Event{}, false
	}
	p := in.PR
	if p.State != "OPEN" {
		e, ok := last(pr.KindMerged, pr.KindClosed)
		since := e.At
		if !ok {
			since = firstNonZero(p.MergedAt, p.ClosedAt)
		}
		return Result{Kind: KindDone, WaitingOn: "nobody", Since: since, Reason: strings.ToLower(p.State)}
	}
	var pushedAt time.Time
	if e, ok := last(pr.KindPush); ok {
		pushedAt = e.At
	} else if e, ok := last(pr.KindOpened); ok {
		pushedAt = e.At
	} else {
		pushedAt = p.CreatedAt
	}
	sent, hasSent := last(pr.KindRoundSent)
	if b, ok := ActiveBlock(evs, in.Viewer); ok {
		return Result{Kind: KindBlocked, WaitingOn: "me", Since: b.At, Reason: "round blocked: " + b.Data}
	}
	if held, ok := last(pr.KindRoundHeld); ok && (!hasSent || held.At.After(sent.At)) && sameDay(held.At, in.Now) {
		return Result{Kind: KindHeldByCap, WaitingOn: "me", Since: held.At, Reason: "automatic round held: " + held.Data}
	}
	if w, ok := last(pr.KindWouldAddress); ok && p.Workspace.ID == "" && !addressedSince(in, evs, w.At) {
		return Result{Kind: KindToAddress, WaitingOn: "me", Since: w.At, Reason: Cut("review to address, no workspace: " + w.Data)}
	}
	if p.Workspace.IsReview {
		if slices.Contains(p.Workspace.AgentStates, agentWaiting) {
			return Result{Kind: KindLoopWaiting, WaitingOn: "me", Since: sent.At, Reason: "the agent asked you something"}
		}
		if slices.Contains(p.Workspace.AgentStates, agentWorking) {
			return Result{Kind: KindLoopWorking, WaitingOn: "me", Since: sent.At, Reason: "review round in progress"}
		}
	}
	ignored := func(actor string) bool {
		return strings.HasSuffix(actor, "[bot]") || slices.ContainsFunc(in.Ignore, func(s string) bool { return strings.EqualFold(s, actor) })
	}
	if p.IsDraft {
		return stalled(Result{Kind: KindDraft, WaitingOn: "author", Since: pushedAt, Reason: "draft"}, 0)
	}
	if p.ChecksState == "FAILURE" {
		return stalled(Result{Kind: KindChecks, WaitingOn: "author", Since: pushedAt, Reason: failingReason(p.FailingChecks)}, in.Stalled.Reply)
	}
	// Newest decisive review per actor on the current head, oldest first. A
	// COMMENTED review does not replace an earlier approval or change request,
	// as on GitHub, but still counts as having reviewed the head.
	var headReviews []pr.Event
	reviewed := map[string]bool{}
	decided := map[string]bool{}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if e.Kind != pr.KindReview || !strings.HasSuffix(e.Data, ":"+p.HeadOid) || ignored(e.Actor) {
			continue
		}
		reviewed[e.Actor] = true
		if strings.HasPrefix(e.Data, "COMMENTED:") || decided[e.Actor] {
			continue
		}
		decided[e.Actor] = true
		headReviews = append([]pr.Event{e}, headReviews...)
	}
	for _, e := range headReviews {
		if strings.HasPrefix(e.Data, "CHANGES_REQUESTED:") {
			return stalled(Result{Kind: KindChanges, WaitingOn: "author", Since: e.At, Reason: "changes requested by " + e.Actor}, in.Stalled.Reply)
		}
	}
	var approval pr.Event
	for _, e := range headReviews {
		if strings.HasPrefix(e.Data, "APPROVED:") {
			approval = e
			break
		}
	}
	approved := p.ReviewDecision == "APPROVED" || (p.ReviewDecision == "" && !approval.At.IsZero())
	if approved {
		since := approval.At
		if since.IsZero() {
			since = pushedAt
			for i := len(evs) - 1; i >= 0; i-- {
				if e := evs[i]; e.Kind == pr.KindReview && strings.HasPrefix(e.Data, "APPROVED:") && !ignored(e.Actor) {
					since = e.At
					break
				}
			}
		}
		if p.Mergeable == "CONFLICTING" {
			if p.MergeableAt.After(since) {
				since = p.MergeableAt
			}
			return stalled(Result{Kind: KindConflicts, WaitingOn: "author", Since: since, Reason: "merge conflicts with " + p.BaseBranch}, in.Stalled.Reply)
		}
		reason := "approved and green"
		if p.ChecksState == "PENDING" {
			reason = "approved, checks pending"
		}
		return stalled(Result{Kind: KindMerge, WaitingOn: "author", Since: since, Reason: reason}, in.Stalled.Merge)
	}
	nudgeComment := nudgeComments(evs, in.Viewer, ignored)
	var lastReviewer, lastAuthor pr.Event
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if (e.Kind != pr.KindReview && e.Kind != pr.KindComment) || ignored(e.Actor) || (e.Kind == pr.KindReview && strings.HasPrefix(e.Data, "APPROVED:")) || nudgeComment[e] {
			continue
		}
		switch {
		case e.Actor == p.Author && lastAuthor.At.IsZero():
			lastAuthor = e
		case e.Actor != p.Author && lastReviewer.At.IsZero():
			lastReviewer = e
		}
	}
	me := func(login string) string {
		if strings.EqualFold(login, in.Viewer) {
			return "me"
		}
		return login
	}
	if !lastReviewer.At.IsZero() && lastReviewer.At.After(pushedAt) {
		if lastAuthor.At.After(lastReviewer.At) {
			return stalled(Result{Kind: KindReviewerBack, WaitingOn: me(lastReviewer.Actor), Since: lastAuthor.At,
				Reason: p.Author + " replied, " + lastReviewer.Actor + " has not returned"}, in.Stalled.Reply)
		}
		return stalled(Result{Kind: KindReply, WaitingOn: "author", Since: lastReviewer.At, Reason: lastReviewer.Actor + " commented, no reply"}, in.Stalled.Reply)
	}
	var pending []string
	for _, r := range p.ReviewRequests {
		if !reviewed[r] && !ignored(r) {
			pending = append(pending, r)
		}
	}
	if len(pending) > 0 {
		since := pushedAt
		for _, e := range evs {
			if e.Kind == pr.KindReviewRequest && slices.Contains(pending, e.Data) && e.At.After(since) {
				since = e.At
			}
		}
		return stalled(Result{Kind: KindReview, WaitingOn: waitingOn(in, pending), Since: since, Reason: "review requested, no review on the head"}, in.Stalled.Review)
	}
	return stalled(Result{Kind: KindNoReviewer, WaitingOn: "author", Since: pushedAt, Reason: "no reviewer requested"}, in.Stalled.Review)
}

// The agent states of a workspace the situation reads.
const (
	agentWorking = "working"
	agentWaiting = "waiting"
)

// ActiveBlock is the newest round_blocked event of evs, oldest first, that
// no later event ended: a round_unblocked, a round sent or held, or a review
// by viewer.
func ActiveBlock(evs []pr.Event, viewer string) (pr.Event, bool) {
	var b pr.Event
	on := false
	for _, e := range evs {
		switch e.Kind {
		case pr.KindRoundBlocked:
			b, on = e, true
		case pr.KindRoundUnblocked, pr.KindRoundSent, pr.KindRoundHeld:
			on = false
		case pr.KindReview:
			if on && viewer != "" && strings.EqualFold(e.Actor, viewer) && e.At.After(b.At) {
				on = false
			}
		}
	}
	return b, on
}

// addressedSince says whether a round, an address turn, a push or a review
// or comment of mine came on the pull request after at.
func addressedSince(in Input, evs []pr.Event, at time.Time) bool {
	if in.AddressedAt.After(at) {
		return true
	}
	return slices.ContainsFunc(evs, func(e pr.Event) bool {
		if !e.At.After(at) {
			return false
		}
		switch e.Kind {
		case pr.KindRoundSent, pr.KindPush:
			return true
		case pr.KindReview, pr.KindComment:
			return strings.EqualFold(e.Actor, in.Viewer)
		}
		return false
	})
}

func waitingOn(in Input, logins []string) string {
	for _, l := range logins {
		if strings.EqualFold(l, in.Viewer) {
			return "me"
		}
		if team, ok := strings.CutPrefix(l, "team:"); ok && slices.ContainsFunc(in.MyTeams, func(t string) bool { return strings.EqualFold(t, team) }) {
			return "me"
		}
	}
	return strings.Join(logins, ", ")
}

// sameDay compares calendar days in now's location: the cap that held a
// round counts rounds per local day.
func sameDay(t, now time.Time) bool {
	y1, m1, d1 := t.In(now.Location()).Date()
	y2, m2, d2 := now.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func stalled(r Result, threshold time.Duration) Result {
	if threshold > 0 && !r.Since.IsZero() {
		r.Stalled = src.After(r.Since, threshold, src.WorkingDays)
	}
	return r
}

func firstNonZero(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// ReasonMax bounds a reason that comes from an agent or a CI, so a row and
// a notification stay one line; the host cuts its own reasons alike.
const ReasonMax = 200

// Cut keeps s within ReasonMax runes.
func Cut(s string) string {
	if r := []rune(s); len(r) > ReasonMax {
		return strings.TrimRight(string(r[:ReasonMax-1]), " ") + "…"
	}
	return s
}

func failingReason(checks []pr.FailingCheck) string {
	if len(checks) == 0 {
		return "checks failing on the head"
	}
	var names []string
	for _, c := range checks[:min(3, len(checks))] {
		names = append(names, c.Name)
	}
	s := "checks failing: " + strings.Join(names, ", ")
	if n := len(checks) - 3; n > 0 {
		s += fmt.Sprintf(" and %d more", n)
	}
	return Cut(s)
}

// NudgeWindow bounds how long after a nudge is asked for its comment can
// be posted: the agent reads the pull request first.
const NudgeWindow = 2 * time.Hour

// nudgeComments are the comments a nudge posted: for each nudge, the user's
// first comment after it, within NudgeWindow, with no comment, review or
// push from anyone else in between. Such a comment is the user asking
// someone else to act, not the user's turn in the conversation.
func nudgeComments(evs []pr.Event, viewer string, ignored func(string) bool) map[pr.Event]bool {
	out := map[pr.Event]bool{}
	activity := func(e pr.Event) bool {
		return (e.Kind == pr.KindComment || e.Kind == pr.KindReview || e.Kind == pr.KindPush) && !ignored(e.Actor)
	}
	for _, n := range evs {
		if n.Kind != pr.KindNudged {
			continue
		}
		from, until := n.At.Add(-time.Minute), n.At.Add(NudgeWindow)
		var first pr.Event
		for _, e := range evs {
			if e.Kind == pr.KindComment && strings.EqualFold(e.Actor, viewer) && !e.At.Before(from) && e.At.Before(until) &&
				(first.At.IsZero() || e.At.Before(first.At)) {
				first = e
			}
		}
		if first.At.IsZero() {
			continue
		}
		if !slices.ContainsFunc(evs, func(e pr.Event) bool {
			return e != first && activity(e) && !e.At.Before(n.At) && e.At.Before(first.At)
		}) {
			out[first] = true
		}
	}
	return out
}
