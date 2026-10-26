// Package situation derives who a pull request is waiting on, and since when,
// from its snapshot and its event history.
package situation

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/workday"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

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

type Input struct {
	PR       dinv.PR
	Events   []dinv.Event
	Override dinv.Override
	Viewer   string   // my login
	MyTeams  []string // "org/slug", any case
	Ignore   []string // actors whose comments and reviews never count: automated reviewers
	Now      time.Time
	Stalled  config.Stalled
	// Turn is the pull request's newest review or address turn, else its
	// newest turn; nil without one.
	Turn *TurnFacts
	// SessionWaiting means Orca shows the session of Turn waiting.
	SessionWaiting bool
}

// TurnFacts is what the loop's store says of a turn.
type TurnFacts struct {
	Intent dintent.Name
	// Role is the role the turn's intent held.
	Role           string
	State, Summary string
	// At is when the turn reached its state; AnsweredAt is when an answer
	// was typed or resumed after its last record, zero without one.
	At, AnsweredAt time.Time
}

type Result struct {
	Kind      Kind
	WaitingOn string // login(s), "me", "author", "nobody"
	Since     time.Time
	Reason    string
	Stalled   bool
}

func Derive(in Input) Result {
	evs := slices.Clone(in.Events)
	slices.SortStableFunc(evs, func(a, b dinv.Event) int { return a.At.Compare(b.At) })
	last := func(kinds ...string) (dinv.Event, bool) {
		for i := len(evs) - 1; i >= 0; i-- {
			if slices.Contains(kinds, evs[i].Kind) {
				return evs[i], true
			}
		}
		return dinv.Event{}, false
	}
	pr := in.PR
	if pr.State != "OPEN" {
		e, ok := last(dinv.KindMerged, dinv.KindClosed)
		since := e.At
		if !ok {
			since = firstNonZero(pr.MergedAt, pr.ClosedAt)
		}
		return Result{Kind: KindDone, WaitingOn: "nobody", Since: since, Reason: strings.ToLower(pr.State)}
	}
	var mutedAt time.Time
	if e, ok := last(dinv.KindMuted); ok {
		mutedAt = e.At
	}
	muted := Result{Kind: KindMuted, WaitingOn: "nobody", Since: mutedAt, Reason: "muted"}
	if o := in.Override; !o.MutedUntil.IsZero() && in.Now.Before(o.MutedUntil) {
		return muted
	}
	if o := in.Override; o.MuteUntilStateChange {
		in.Override.MuteUntilStateChange = false
		r := Derive(in)
		if string(r.Kind) == o.MutedKind && sameWaitingOn(r.WaitingOn, o.MutedWaitingOn) {
			return muted
		}
		return r
	}
	var pushedAt time.Time
	if e, ok := last(dinv.KindPush); ok {
		pushedAt = e.At
	} else if e, ok := last(dinv.KindOpened); ok {
		pushedAt = e.At
	} else {
		pushedAt = pr.CreatedAt
	}
	sent, hasSent := last(dinv.KindRoundSent)
	if b, ok := dinv.ActiveBlock(evs, in.Viewer); ok {
		return Result{Kind: KindBlocked, WaitingOn: "me", Since: b.At, Reason: "round blocked: " + b.Data}
	}
	if held, ok := last(dinv.KindRoundHeld); ok && (!hasSent || held.At.After(sent.At)) && sameDay(held.At, in.Now) {
		return Result{Kind: KindHeldByCap, WaitingOn: "me", Since: held.At, Reason: "automatic round held: " + held.Data}
	}
	if w, ok := last(dinv.KindWouldAddress); ok && pr.Workspace.ID == "" && !addressedSince(in, evs, w.At) {
		return Result{Kind: KindToAddress, WaitingOn: "me", Since: w.At, Reason: cut("review to address, no workspace: " + w.Data)}
	}
	if r, ok := turnSituation(in); ok {
		return r
	}
	if pr.Workspace.IsReview {
		if slices.Contains(pr.Workspace.AgentStates, turn.AgentWaiting) {
			return Result{Kind: KindLoopWaiting, WaitingOn: "me", Since: sent.At, Reason: "the agent asked you something"}
		}
		if slices.Contains(pr.Workspace.AgentStates, turn.AgentWorking) {
			return Result{Kind: KindLoopWorking, WaitingOn: "me", Since: sent.At, Reason: "review round in progress"}
		}
	}
	ignored := func(actor string) bool {
		return strings.HasSuffix(actor, "[bot]") || slices.ContainsFunc(in.Ignore, func(s string) bool { return strings.EqualFold(s, actor) })
	}
	if pr.IsDraft {
		return stalled(in, Result{Kind: KindDraft, WaitingOn: "author", Since: pushedAt, Reason: "draft"}, 0)
	}
	if pr.ChecksState == "FAILURE" {
		return stalled(in, Result{Kind: KindChecks, WaitingOn: "author", Since: pushedAt, Reason: failingReason(pr.FailingChecks)}, time.Duration(in.Stalled.Reply))
	}
	// Newest decisive review per actor on the current head, oldest first. A
	// COMMENTED review does not replace an earlier approval or change request,
	// as on GitHub, but still counts as having reviewed the head.
	var headReviews []dinv.Event
	reviewed := map[string]bool{}
	decided := map[string]bool{}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if e.Kind != dinv.KindReview || !strings.HasSuffix(e.Data, ":"+pr.HeadOid) || ignored(e.Actor) {
			continue
		}
		reviewed[e.Actor] = true
		if strings.HasPrefix(e.Data, "COMMENTED:") || decided[e.Actor] {
			continue
		}
		decided[e.Actor] = true
		headReviews = append([]dinv.Event{e}, headReviews...)
	}
	for _, e := range headReviews {
		if strings.HasPrefix(e.Data, "CHANGES_REQUESTED:") {
			return stalled(in, Result{Kind: KindChanges, WaitingOn: "author", Since: e.At, Reason: "changes requested by " + e.Actor}, time.Duration(in.Stalled.Reply))
		}
	}
	var approval dinv.Event
	for _, e := range headReviews {
		if strings.HasPrefix(e.Data, "APPROVED:") {
			approval = e
			break
		}
	}
	approved := pr.ReviewDecision == "APPROVED" || (pr.ReviewDecision == "" && !approval.At.IsZero())
	if approved {
		since := approval.At
		if since.IsZero() {
			since = pushedAt
			for i := len(evs) - 1; i >= 0; i-- {
				if e := evs[i]; e.Kind == dinv.KindReview && strings.HasPrefix(e.Data, "APPROVED:") && !ignored(e.Actor) {
					since = e.At
					break
				}
			}
		}
		if pr.Mergeable == "CONFLICTING" {
			if pr.MergeableAt.After(since) {
				since = pr.MergeableAt
			}
			return stalled(in, Result{Kind: KindConflicts, WaitingOn: "author", Since: since, Reason: "merge conflicts with " + pr.BaseBranch}, time.Duration(in.Stalled.Reply))
		}
		reason := "approved and green"
		if pr.ChecksState == "PENDING" {
			reason = "approved, checks pending"
		}
		return stalled(in, Result{Kind: KindMerge, WaitingOn: "author", Since: since, Reason: reason}, time.Duration(in.Stalled.Merge))
	}
	nudgeComment := nudgeComments(evs, in.Viewer, ignored)
	var lastReviewer, lastAuthor dinv.Event
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		if (e.Kind != dinv.KindReview && e.Kind != dinv.KindComment) || ignored(e.Actor) || (e.Kind == dinv.KindReview && strings.HasPrefix(e.Data, "APPROVED:")) || nudgeComment[e] {
			continue
		}
		switch {
		case e.Actor == pr.Author && lastAuthor.At.IsZero():
			lastAuthor = e
		case e.Actor != pr.Author && lastReviewer.At.IsZero():
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
			return stalled(in, Result{Kind: KindReviewerBack, WaitingOn: me(lastReviewer.Actor), Since: lastAuthor.At,
				Reason: pr.Author + " replied, " + lastReviewer.Actor + " has not returned"}, time.Duration(in.Stalled.Reply))
		}
		return stalled(in, Result{Kind: KindReply, WaitingOn: "author", Since: lastReviewer.At, Reason: lastReviewer.Actor + " commented, no reply"}, time.Duration(in.Stalled.Reply))
	}
	var pending []string
	for _, r := range pr.ReviewRequests {
		if !reviewed[r] && !ignored(r) {
			pending = append(pending, r)
		}
	}
	if len(pending) > 0 {
		since := pushedAt
		for _, e := range evs {
			if e.Kind == dinv.KindReviewRequest && slices.Contains(pending, e.Reviewer()) && e.At.After(since) {
				since = e.At
			}
		}
		return stalled(in, Result{Kind: KindReview, WaitingOn: waitingOn(in, pending), Since: since, Reason: "review requested, no review on the head"}, time.Duration(in.Stalled.Review))
	}
	return stalled(in, Result{Kind: KindNoReviewer, WaitingOn: "author", Since: pushedAt, Reason: "no reviewer requested"}, time.Duration(in.Stalled.Review))
}

// addressedSince says whether a round, an address turn, a push or a review
// or comment of mine came on the pull request after at.
func addressedSince(in Input, evs []dinv.Event, at time.Time) bool {
	if in.Turn != nil && in.Turn.Role == builtin.RoleAddress && in.Turn.At.After(at) {
		return true
	}
	return slices.ContainsFunc(evs, func(e dinv.Event) bool {
		if !e.At.After(at) {
			return false
		}
		switch e.Kind {
		case dinv.KindRoundSent, dinv.KindPush:
			return true
		case dinv.KindReview, dinv.KindComment:
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

func sameWaitingOn(a, b string) bool {
	set := func(s string) []string {
		v := strings.Split(s, ", ")
		slices.Sort(v)
		return v
	}
	return slices.Equal(set(a), set(b))
}

// sameDay compares calendar days in now's location: the cap that held a
// round counts rounds per local day.
func sameDay(t, now time.Time) bool {
	y1, m1, d1 := t.In(now.Location()).Date()
	y2, m2, d2 := now.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func stalled(in Input, r Result, threshold time.Duration) Result {
	if threshold > 0 && !r.Since.IsZero() && workday.Between(r.Since, in.Now) > threshold {
		r.Stalled = true
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
// a notification stay one line.
const ReasonMax = 200

// turnSituation is the situation the loop's turn decides; ok is false when the
// turn leaves it to GitHub and Orca's agent states.
func turnSituation(in Input) (Result, bool) {
	t := in.Turn
	if t == nil || (t.Role != builtin.RoleReview && t.Role != builtin.RoleAddress) {
		return Result{}, false
	}
	at := func(k Kind, reason string) (Result, bool) {
		return Result{Kind: k, WaitingOn: "me", Since: t.At, Reason: cut(reason)}, true
	}
	switch t.State {
	case "asked":
		if t.AnsweredAt.After(t.At) {
			return at(KindLoopWorking, "your answer was sent")
		}
		return at(KindLoopWaiting, t.Summary)
	case "failed":
		return at(KindLoopStopped, t.Summary)
	case "unrecorded":
		if t.Summary == "" {
			return at(KindLoopStopped, "the agent ended its turn without a record")
		}
		return at(KindLoopStopped, "the agent ended its turn without a record: "+t.Summary)
	case "started":
		if in.SessionWaiting {
			return at(KindLoopWaiting, "waiting for a permission in the terminal")
		}
		return at(KindLoopWorking, string(t.Intent)+" round in progress")
	}
	return Result{}, false
}

func failingReason(checks []dinv.FailingCheck) string {
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
	return cut(s)
}

// cut keeps s within ReasonMax runes.
func cut(s string) string {
	if r := []rune(s); len(r) > ReasonMax {
		return strings.TrimRight(string(r[:ReasonMax-1]), " ") + "…"
	}
	return s
}

// NudgeWindow bounds how long after a nudge is asked for its comment can
// be posted: the agent reads the pull request first.
const NudgeWindow = 2 * time.Hour

// nudgeComments are the comments a nudge posted: for each nudge, the user's
// first comment after it, within NudgeWindow, with no comment, review or
// push from anyone else in between. Such a comment is the user asking
// someone else to act, not the user's turn in the conversation.
func nudgeComments(evs []dinv.Event, viewer string, ignored func(string) bool) map[dinv.Event]bool {
	out := map[dinv.Event]bool{}
	activity := func(e dinv.Event) bool {
		return (e.Kind == dinv.KindComment || e.Kind == dinv.KindReview || e.Kind == dinv.KindPush) && !ignored(e.Actor)
	}
	for _, n := range evs {
		if n.Kind != dinv.KindNudged {
			continue
		}
		from, until := n.At.Add(-time.Minute), n.At.Add(NudgeWindow)
		var first dinv.Event
		for _, e := range evs {
			if e.Kind == dinv.KindComment && strings.EqualFold(e.Actor, viewer) && !e.At.Before(from) && e.At.Before(until) &&
				(first.At.IsZero() || e.At.Before(first.At)) {
				first = e
			}
		}
		if first.At.IsZero() {
			continue
		}
		if !slices.ContainsFunc(evs, func(e dinv.Event) bool {
			return e != first && activity(e) && !e.At.Before(n.At) && e.At.Before(first.At)
		}) {
			out[first] = true
		}
	}
	return out
}
