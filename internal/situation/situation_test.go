package situation

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

var (
	ref = github.Ref{Owner: "acme", Repo: "api", Number: 7}
	t0  = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC) // Monday
	th  = config.Stalled{Review: config.Days(48 * time.Hour), Reply: config.Days(24 * time.Hour), Merge: config.Days(24 * time.Hour)}
)

func h(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }

func ev(kind, key, actor string, at time.Time, data string) dinv.Event {
	return dinv.Event{PR: ref, Kind: kind, Key: key, Actor: actor, At: at, Data: data}
}

func requested(reviewer, actor string, at time.Time) dinv.Event {
	return ev(dinv.KindReviewRequest, reviewer+":"+at.UTC().Format(time.RFC3339), actor, at, reviewer)
}

func pr(author string, requested ...string) dinv.PR {
	return dinv.PR{Ref: ref, Author: author, State: "OPEN", HeadOid: "h2", ChecksState: "SUCCESS", ReviewRequests: requested}
}

func derive(p dinv.PR, now time.Time, evs ...dinv.Event) Result {
	return Derive(Input{PR: p, Events: evs, Viewer: "me", MyTeams: []string{"Acme/search"}, Ignore: []string{"sonar"}, Now: now, Stalled: th})
}

func TestDraftWaitsOnAuthor(t *testing.T) {
	p := pr("ana")
	p.IsDraft = true
	r := derive(p, h(10), ev(dinv.KindOpened, "", "ana", h(0), ""), ev(dinv.KindPush, "h2", "ana", h(1), ""))
	if r.Kind != KindDraft || r.WaitingOn != "author" || !r.Since.Equal(h(1)) {
		t.Fatalf("%+v", r)
	}
}

func TestFailingChecksWaitOnAuthor(t *testing.T) {
	p := pr("ana", "me")
	p.ChecksState = "FAILURE"
	r := derive(p, h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("me", "ana", h(2)))
	if r.Kind != KindChecks || r.WaitingOn != "author" || !r.Since.Equal(h(1)) {
		t.Fatalf("%+v", r)
	}
}

func TestFailingChecksAreNamedInTheReason(t *testing.T) {
	p := pr("ana")
	p.ChecksState = "FAILURE"
	p.FailingChecks = []dinv.FailingCheck{{Name: "Build and test"}, {Name: "lint"}, {Name: "e2e"}, {Name: "docs"}}
	if r := derive(p, h(10)); r.Kind != KindChecks || r.Reason != "checks failing: Build and test, lint, e2e and 1 more" {
		t.Fatalf("%+v", r)
	}
	p.FailingChecks = []dinv.FailingCheck{{Name: strings.Repeat("x", ReasonMax)}}
	if r := derive(p, h(10)); utf8.RuneCountInString(r.Reason) != ReasonMax || !strings.HasSuffix(r.Reason, "…") {
		t.Fatalf("not cut: %q", r.Reason)
	}
	p.FailingChecks = nil
	if r := derive(p, h(10)); r.Reason != "checks failing on the head" {
		t.Fatalf("no names: %+v", r)
	}
}

func TestChangesRequestedOnHeadWaitsOnAuthor(t *testing.T) {
	r := derive(pr("ana"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), ev(dinv.KindReview, "r1", "me", h(3), "CHANGES_REQUESTED:h2"))
	if r.Kind != KindChanges || r.WaitingOn != "author" || !r.Since.Equal(h(3)) {
		t.Fatalf("%+v", r)
	}
}

func TestChangesRequestedIsDeterministicWithTwoReviewers(t *testing.T) {
	evs := []dinv.Event{ev(dinv.KindPush, "h2", "ana", h(1), ""),
		ev(dinv.KindReview, "r1", "carla", h(3), "CHANGES_REQUESTED:h2"), ev(dinv.KindReview, "r2", "me", h(4), "CHANGES_REQUESTED:h2")}
	for i := 0; i < 20; i++ {
		r := derive(pr("ana"), h(10), evs...)
		if !r.Since.Equal(h(3)) || r.Reason != "changes requested by carla" {
			t.Fatalf("run %d: %+v", i, r)
		}
	}
}

func TestReviewRequestedWaitsOnReviewer(t *testing.T) {
	r := derive(pr("ana", "carla"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("carla", "ana", h(2)))
	if r.Kind != KindReview || r.WaitingOn != "carla" || !r.Since.Equal(h(2)) {
		t.Fatalf("%+v", r)
	}
}

func TestAReRequestWaitsSinceTheLatestRequest(t *testing.T) {
	first, again := requested("carla", "ana", h(2)), requested("carla", "ana", h(5))
	if first.Key == again.Key || first.Key != "carla:"+h(2).UTC().Format(time.RFC3339) {
		t.Fatalf("keys %q %q", first.Key, again.Key)
	}
	r := derive(pr("ana", "carla"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), first, again)
	if r.Kind != KindReview || r.WaitingOn != "carla" || !r.Since.Equal(h(5)) {
		t.Fatalf("%+v", r)
	}
}

func TestRemovedRequestIsNotPending(t *testing.T) {
	r := derive(pr("ana"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("carla", "ana", h(2)))
	if r.Kind != KindNoReviewer {
		t.Fatalf("%+v", r)
	}
}

func TestWaitingOnMeWhenRequestedByNameAndTeam(t *testing.T) {
	r := derive(pr("ana", "me", "team:acme/search"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""),
		requested("me", "ana", h(2)), requested("team:acme/search", "ana", h(2)))
	if r.Kind != KindReview || r.WaitingOn != "me" {
		t.Fatalf("%+v", r)
	}
}

func TestTeamRequestMatchesMyTeamCaseInsensitively(t *testing.T) {
	r := derive(pr("ana", "team:ACME/Search"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("team:ACME/Search", "ana", h(2)))
	if r.WaitingOn != "me" {
		t.Fatalf("%+v", r)
	}
}

func TestPushAfterRequestRestartsTheClock(t *testing.T) {
	r := derive(pr("ana", "carla"), h(10), requested("carla", "ana", h(1)), ev(dinv.KindPush, "h2", "ana", h(4), ""))
	if r.Kind != KindReview || !r.Since.Equal(h(4)) {
		t.Fatalf("%+v", r)
	}
}

func TestNoReviewerWaitsOnAuthor(t *testing.T) {
	r := derive(pr("ana"), h(10), ev(dinv.KindOpened, "", "ana", h(0), ""), ev(dinv.KindPush, "h2", "ana", h(1), ""))
	if r.Kind != KindNoReviewer || r.WaitingOn != "author" || !r.Since.Equal(h(1)) {
		t.Fatalf("%+v", r)
	}
}

func TestApprovedAndGreenCanMerge(t *testing.T) {
	p := pr("ana")
	p.ReviewDecision = "APPROVED"
	r := derive(p, h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), ev(dinv.KindReview, "r1", "me", h(3), "APPROVED:h2"), ev(dinv.KindReview, "r2", "me", h(5), "COMMENTED:h2"))
	if r.Kind != KindMerge || r.WaitingOn != "author" || !r.Since.Equal(h(3)) {
		t.Fatalf("%+v", r)
	}
}

func TestConflictsComeBeforeTheMergeButton(t *testing.T) {
	p := pr("ana")
	p.ReviewDecision = "APPROVED"
	p.BaseBranch = "main"
	evs := []dinv.Event{ev(dinv.KindPush, "h2", "ana", h(1), ""), ev(dinv.KindReview, "r1", "me", h(3), "APPROVED:h2")}
	for mergeable, want := range map[string]Kind{"CONFLICTING": KindConflicts, "UNKNOWN": KindMerge, "": KindMerge, "MERGEABLE": KindMerge} {
		p.Mergeable = mergeable
		r := derive(p, h(10), evs...)
		if r.Kind != want || r.WaitingOn != "author" {
			t.Errorf("%q: %+v", mergeable, r)
		}
		if want == KindConflicts && (r.Reason != "merge conflicts with main" || !r.Since.Equal(h(3))) {
			t.Errorf("%+v", r)
		}
	}
	p.Mergeable, p.MergeableAt = "CONFLICTING", h(8)
	if r := derive(p, h(10), evs...); r.Kind != KindConflicts || !r.Since.Equal(h(8)) {
		t.Fatalf("a conflict dated before it appeared: %+v", r)
	}
	p.Mergeable, p.ReviewDecision = "CONFLICTING", "REVIEW_REQUIRED"
	p.ReviewRequests = []string{"bia"}
	if r := derive(p, h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("bia", "ana", h(1))); r.Kind != KindReview {
		t.Fatalf("a conflict took the review from its reviewer: %+v", r)
	}
	if KindConflicts.Label() != "merge conflicts" {
		t.Fatalf("label %q", KindConflicts.Label())
	}
}

func TestApprovedWithoutRequiredReviewsCanMerge(t *testing.T) {
	r := derive(pr("ana"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), ev(dinv.KindReview, "r1", "carla", h(3), "APPROVED:h2"))
	if r.Kind != KindMerge || !r.Since.Equal(h(3)) {
		t.Fatalf("%+v", r)
	}
}

func TestReviewerCommentsWaitOnAuthor(t *testing.T) {
	r := derive(pr("ana", "me"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("me", "ana", h(2)),
		ev(dinv.KindReview, "r1", "me", h(3), "COMMENTED:h2"))
	if r.Kind != KindReply || r.WaitingOn != "author" || !r.Since.Equal(h(3)) {
		t.Fatalf("%+v", r)
	}
}

func TestAuthorReplyWaitsOnReviewer(t *testing.T) {
	r := derive(pr("ana", "me"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("me", "ana", h(2)),
		ev(dinv.KindReview, "r1", "me", h(3), "COMMENTED:h2"), ev(dinv.KindComment, "c1", "ana", h(5), ""))
	if r.Kind != KindReviewerBack || r.WaitingOn != "me" || !r.Since.Equal(h(5)) {
		t.Fatalf("%+v", r)
	}
}

func TestBotCommentsAreIgnored(t *testing.T) {
	r := derive(pr("ana", "carla"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("carla", "ana", h(2)),
		ev(dinv.KindComment, "c1", "codecov[bot]", h(3), ""), ev(dinv.KindComment, "c2", "sonar", h(4), ""))
	if r.Kind != KindReview || r.WaitingOn != "carla" {
		t.Fatalf("%+v", r)
	}
}

func TestLoopWorkingAndWaiting(t *testing.T) {
	p := pr("ana")
	p.Workspace = dinv.Workspace{ID: "w", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWorking}}
	r := derive(p, h(10), ev(dinv.KindRoundSent, "", "me", h(9), ""))
	if r.Kind != KindLoopWorking || r.WaitingOn != "me" || !r.Since.Equal(h(9)) {
		t.Fatalf("%+v", r)
	}
	p.Workspace.AgentStates = []turn.AgentState{turn.AgentWaiting}
	r = derive(p, h(10), ev(dinv.KindRoundSent, "", "me", h(9), ""))
	if r.Kind != KindLoopWaiting || r.WaitingOn != "me" {
		t.Fatalf("%+v", r)
	}
}

func TestHeldByCap(t *testing.T) {
	r := derive(pr("ana", "me"), h(10), requested("me", "ana", h(1)), ev(dinv.KindRoundHeld, "", "me", h(9), "5 rounds today"))
	if r.Kind != KindHeldByCap || r.WaitingOn != "me" || !r.Since.Equal(h(9)) {
		t.Fatalf("%+v", r)
	}
	r = derive(pr("ana", "me"), h(10), requested("me", "ana", h(1)), ev(dinv.KindRoundHeld, "", "me", h(8), "x"), ev(dinv.KindRoundSent, "", "me", h(9), ""))
	if r.Kind == KindHeldByCap {
		t.Fatalf("still held after a round was sent: %+v", r)
	}
}

func TestDoneAndMuted(t *testing.T) {
	p := pr("ana")
	p.State = "MERGED"
	if r := derive(p, h(10), ev(dinv.KindMerged, "", "", h(9), "")); r.Kind != KindDone || r.WaitingOn != "nobody" || !r.Since.Equal(h(9)) {
		t.Fatalf("%+v", r)
	}
	p = pr("ana", "me")
	evs := []dinv.Event{requested("me", "ana", h(1))}
	r := Derive(Input{PR: p, Events: evs, Override: dinv.Override{PR: ref, MutedUntil: h(20)}, Viewer: "me", Now: h(10), Stalled: th})
	if r.Kind != KindMuted || r.Stalled {
		t.Fatalf("%+v", r)
	}
	r = Derive(Input{PR: p, Events: evs, Override: dinv.Override{PR: ref, MutedUntil: h(5)}, Viewer: "me", Now: h(10), Stalled: th})
	if r.Kind == KindMuted {
		t.Fatalf("expired mute still applies: %+v", r)
	}
}

func TestMutedSinceIsTheMuteTime(t *testing.T) {
	p := pr("ana", "me")
	evs := []dinv.Event{requested("me", "ana", h(1)), ev(dinv.KindMuted, "", "me", h(4), "")}
	o := dinv.Override{PR: ref, MutedUntil: h(20)}
	for _, now := range []time.Time{h(10), h(11)} {
		r := Derive(Input{PR: p, Events: evs, Override: o, Viewer: "me", Now: now, Stalled: th})
		if r.Kind != KindMuted || !r.Since.Equal(h(4)) {
			t.Fatalf("at %v: %+v", now, r)
		}
	}
	r := Derive(Input{PR: p, Events: evs[:1], Override: o, Viewer: "me", Now: h(10), Stalled: th})
	if r.Kind != KindMuted || !r.Since.IsZero() {
		t.Fatalf("no muted event: %+v", r)
	}
}

func TestMuteUntilStateChangeHoldsWhileTheSituationStays(t *testing.T) {
	p := pr("ana", "me")
	evs := []dinv.Event{requested("me", "ana", h(1))}
	o := dinv.Override{PR: ref, MuteUntilStateChange: true, MutedKind: string(KindReview), MutedWaitingOn: "me"}
	r := Derive(Input{PR: p, Events: evs, Override: o, Viewer: "me", Now: h(100), Stalled: th})
	if r.Kind != KindMuted || r.Stalled {
		t.Fatalf("%+v", r)
	}
}

func TestMuteUntilStateChangeExpiresWhenTheSituationChanges(t *testing.T) {
	o := dinv.Override{PR: ref, MuteUntilStateChange: true, MutedKind: string(KindReview), MutedWaitingOn: "me"}
	evs := []dinv.Event{requested("me", "ana", h(1))}
	changed := append(evs, ev(dinv.KindReview, "r1", "me", h(2), "CHANGES_REQUESTED:h2"))
	if r := Derive(Input{PR: pr("ana", "me"), Events: changed, Override: o, Viewer: "me", Now: h(10), Stalled: th}); r.Kind != KindChanges {
		t.Fatalf("kind changed, mute still applies: %+v", r)
	}
	if r := Derive(Input{PR: pr("ana", "carla"), Events: evs, Override: o, Viewer: "me", Now: h(10), Stalled: th}); r.Kind != KindReview || r.WaitingOn != "carla" {
		t.Fatalf("waiting-on changed, mute still applies: %+v", r)
	}
}

func TestSituationForkIsDerived(t *testing.T) {
	p := pr("ext", "me")
	p.IsFork = true
	r := derive(p, h(10), requested("me", "ext", h(1)))
	if r.Kind != KindReview || r.WaitingOn != "me" {
		t.Fatalf("%+v", r)
	}
}

func TestStalledSkipsWeekend(t *testing.T) {
	friday := time.Date(2026, 10, 2, 17, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	r := derive(pr("ana", "carla"), monday, requested("carla", "ana", friday))
	if r.Stalled {
		t.Fatalf("stalled across the weekend: %+v", r)
	}
	r = derive(pr("ana", "carla"), monday.Add(48*time.Hour), requested("carla", "ana", friday))
	if !r.Stalled {
		t.Fatalf("not stalled after two working days: %+v", r)
	}
}

func TestApprovalWithPendingChecksIsNotAComment(t *testing.T) {
	p := pr("ana")
	p.ReviewDecision = "APPROVED"
	p.ChecksState = "PENDING"
	r := derive(p, h(40), ev(dinv.KindPush, "h2", "ana", h(1), ""), ev(dinv.KindReview, "r1", "carla", h(3), "APPROVED:h2"))
	if r.Kind != KindMerge || r.WaitingOn != "author" || !r.Since.Equal(h(3)) || r.Reason != "approved, checks pending" {
		t.Fatalf("%+v", r)
	}
}

func TestApprovalUnderRequiredReviewWaitsOnPendingReviewer(t *testing.T) {
	p := pr("ana", "dave")
	p.ReviewDecision = "REVIEW_REQUIRED"
	evs := []dinv.Event{ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("dave", "ana", h(2)),
		ev(dinv.KindReview, "r1", "carla", h(3), "APPROVED:h2")}
	r := derive(p, h(10), evs...)
	if r.Kind != KindReview || r.WaitingOn != "dave" || !r.Since.Equal(h(2)) {
		t.Fatalf("%+v", r)
	}
	r = derive(p, h(10), append(evs, ev(dinv.KindComment, "c1", "ana", h(4), "thanks"))...)
	if r.Kind != KindReview || r.WaitingOn != "dave" {
		t.Fatalf("after the author thanked the approver: %+v", r)
	}
}

func TestApprovalOnOlderHeadStartsTheMergeClock(t *testing.T) {
	p := pr("ana")
	p.ReviewDecision = "APPROVED"
	r := derive(p, h(10), ev(dinv.KindPush, "h1", "ana", h(0), ""), ev(dinv.KindReview, "r1", "carla", h(2), "APPROVED:h1"),
		ev(dinv.KindPush, "h2", "ana", h(3), ""), ev(dinv.KindReview, "r2", "codecov[bot]", h(9), "COMMENTED:h2"),
		ev(dinv.KindReview, "r3", "dave", h(8), "COMMENTED:h1"))
	if r.Kind != KindMerge || !r.Since.Equal(h(2)) {
		t.Fatalf("%+v", r)
	}
}

func TestApprovedWithoutAnyApprovalUsesLastPush(t *testing.T) {
	p := pr("ana")
	p.ReviewDecision = "APPROVED"
	r := derive(p, h(10), ev(dinv.KindPush, "h2", "ana", h(3), ""), ev(dinv.KindReview, "r1", "codecov[bot]", h(9), "COMMENTED:h2"))
	if r.Kind != KindMerge || !r.Since.Equal(h(3)) {
		t.Fatalf("%+v", r)
	}
}

func TestIgnoredRequestedReviewerIsNotPending(t *testing.T) {
	r := derive(pr("ana", "sonar"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), requested("sonar", "ana", h(2)))
	if r.Kind != KindNoReviewer {
		t.Fatalf("%+v", r)
	}
	r = derive(pr("ana", "sonar", "carla", "renovate[bot]"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""),
		requested("carla", "ana", h(2)), requested("sonar", "ana", h(5)),
		requested("renovate[bot]", "ana", h(6)))
	if r.Kind != KindReview || r.WaitingOn != "carla" || !r.Since.Equal(h(2)) {
		t.Fatalf("%+v", r)
	}
}

func TestCommentAfterChangesRequestedKeepsTheChangeRequest(t *testing.T) {
	r := derive(pr("ana"), h(10), ev(dinv.KindPush, "h2", "ana", h(1), ""), ev(dinv.KindReview, "r1", "carla", h(3), "CHANGES_REQUESTED:h2"),
		ev(dinv.KindReview, "r2", "carla", h(5), "COMMENTED:h2"))
	if r.Kind != KindChanges || r.Reason != "changes requested by carla" || !r.Since.Equal(h(3)) {
		t.Fatalf("%+v", r)
	}
}

func TestSinceFallsBackWithoutEvents(t *testing.T) {
	p := pr("ana")
	p.CreatedAt = h(1)
	if r := derive(p, h(10)); r.Kind != KindNoReviewer || !r.Since.Equal(h(1)) {
		t.Fatalf("open without a push: %+v", r)
	}
	p.State, p.MergedAt = "MERGED", h(7)
	if r := derive(p, h(10)); r.Kind != KindDone || !r.Since.Equal(h(7)) {
		t.Fatalf("merged without an event: %+v", r)
	}
	p.State, p.MergedAt, p.ClosedAt = "CLOSED", time.Time{}, h(8)
	if r := derive(p, h(10)); r.Kind != KindDone || !r.Since.Equal(h(8)) {
		t.Fatalf("closed without an event: %+v", r)
	}
}

func TestHeldByCapLastsOnlyTheLocalDayItWasWritten(t *testing.T) {
	requested := requested("me", "ana", h(1))
	if r := derive(pr("ana", "me"), h(33), requested, ev(dinv.KindRoundHeld, "", "me", h(9), "5 rounds today")); r.Kind == KindHeldByCap {
		t.Fatalf("a round held yesterday still holds: %+v", r)
	}
	brt := time.FixedZone("BRT", -3*3600)
	heldLate := time.Date(2026, 10, 5, 23, 0, 0, 0, brt) // Tuesday 02:00 UTC
	nextMorning := time.Date(2026, 10, 6, 9, 0, 0, 0, brt)
	if r := derive(pr("ana", "me"), nextMorning, requested, ev(dinv.KindRoundHeld, "", "me", heldLate.UTC(), "x")); r.Kind == KindHeldByCap {
		t.Fatalf("held on the previous local day still holds: %+v", r)
	}
	if r := derive(pr("ana", "me"), heldLate.Add(30*time.Minute), requested, ev(dinv.KindRoundHeld, "", "me", heldLate.UTC(), "x")); r.Kind != KindHeldByCap {
		t.Fatalf("held today does not hold: %+v", r)
	}
}

func TestMuteUntilStateComparesWaitingOnInAnyOrder(t *testing.T) {
	o := dinv.Override{PR: ref, MuteUntilStateChange: true, MutedKind: string(KindReview), MutedWaitingOn: "carla, bruno"}
	evs := []dinv.Event{requested("carla", "ana", h(1))}
	if r := Derive(Input{PR: pr("ana", "bruno", "carla"), Events: evs, Override: o, Viewer: "me", Now: h(10), Stalled: th}); r.Kind != KindMuted {
		t.Fatalf("reordered reviewers unmuted: %+v", r)
	}
}

func TestTeamRequestWaitsOnMeOnlyForMyTeams(t *testing.T) {
	requested := requested("team:acme/platform", "ana", h(1))
	if r := derive(pr("ana", "team:acme/platform"), h(10), requested); r.Kind != KindReview || r.WaitingOn != "team:acme/platform" {
		t.Fatalf("a followed team's request: %+v", r)
	}
	requested.Key = "team:acme/search"
	if r := derive(pr("ana", "team:acme/search"), h(10), requested); r.WaitingOn != "me" {
		t.Fatalf("my team's request: %+v", r)
	}
}

func TestOrcaWaitingOnAnAuthorWorkspaceChangesNothing(t *testing.T) {
	p := pr("me", "carla")
	p.Workspace = dinv.Workspace{ID: "w", Status: "in-review", AgentStates: []turn.AgentState{turn.AgentWaiting}}
	r := derive(p, h(10), requested("carla", "me", h(1)))
	if r.Kind != KindReview || r.WaitingOn != "carla" {
		t.Fatalf("a permission prompt surfaced: %+v", r)
	}
}

func TestFirstRoundBlocked(t *testing.T) {
	requested := requested("me", "ana", h(1))
	blocked := ev(dinv.KindRoundBlocked, "", "me", h(9), "feat/x is checked out in /src/api")
	for _, now := range []time.Time{h(10), h(40)} {
		r := derive(pr("ana", "me"), now, requested, blocked)
		if r.Kind != KindBlocked || r.WaitingOn != "me" || !r.Since.Equal(h(9)) || r.Reason != "round blocked: feat/x is checked out in /src/api" {
			t.Fatalf("at %v: %+v", now, r)
		}
	}
	for name, later := range map[string]dinv.Event{
		"unblocked":    ev(dinv.KindRoundUnblocked, "", "me", h(10), "the branch is free"),
		"round sent":   ev(dinv.KindRoundSent, "", "me", h(10), ""),
		"my review":    ev(dinv.KindReview, "r9", "me", h(10), "COMMENTED:h2"),
		"blocked held": ev(dinv.KindRoundHeld, "", "me", h(8), "x"),
		"push":         ev(dinv.KindPush, "h3", "ana", h(10), ""),
	} {
		r := derive(pr("ana", "me"), h(11), requested, blocked, later)
		if (r.Kind == KindBlocked) != (name == "blocked held" || name == "push") {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	if r := derive(pr("ana", "me"), h(11), requested, blocked, ev(dinv.KindReview, "r8", "bob", h(10), "COMMENTED:h2")); r.Kind != KindBlocked {
		t.Fatalf("another reviewer cleared my block: %+v", r)
	}
}

func TestAReviewToAddressWithoutAWorkspaceNeedsMe(t *testing.T) {
	review := ev(dinv.KindReview, "r1", "bob", h(2), "CHANGES_REQUESTED:h2")
	would := ev(dinv.KindWouldAddress, "2026-10-08T10:00:00Z", "", h(3), "reviewed by bob")
	r := derive(pr("me", "bob"), h(4), review, would)
	if r.Kind != KindToAddress || r.WaitingOn != "me" || !r.Since.Equal(h(3)) || r.Reason != "review to address, no workspace: reviewed by bob" {
		t.Fatalf("%+v", r)
	}
	if r := derive(pr("me", "bob"), h(4), review, would, ev(dinv.KindRoundSent, "", "", h(4), "reviewed by bob")); r.Kind == KindToAddress {
		t.Fatalf("a round ran and it still waits: %+v", r)
	}
	for name, later := range map[string]dinv.Event{
		"push":            ev(dinv.KindPush, "h3", "me", h(4), ""),
		"my comment":      ev(dinv.KindComment, "c1", "me", h(4), ""),
		"my review":       ev(dinv.KindReview, "r2", "me", h(4), "COMMENTED:h2"),
		"bob's comment":   ev(dinv.KindComment, "c2", "bob", h(4), ""),
		"an earlier push": ev(dinv.KindPush, "h2", "me", h(1), ""),
	} {
		r := derive(pr("me", "bob"), h(5), review, would, later)
		if (r.Kind == KindToAddress) != (name == "bob's comment" || name == "an earlier push") {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	withWS := pr("me", "bob")
	withWS.Workspace.ID = "ws-1"
	if r := derive(withWS, h(4), review, would); r.Kind == KindToAddress {
		t.Fatalf("the workspace exists and it still waits: %+v", r)
	}
	turn := Derive(Input{PR: pr("me", "bob"), Events: []dinv.Event{review, would}, Viewer: "me", Now: h(5), Stalled: th,
		Turn: &TurnFacts{Intent: "address", Role: "pr.address", State: "done", At: h(4)}})
	if turn.Kind == KindToAddress {
		t.Fatalf("an address turn ran and it still waits: %+v", turn)
	}
	if got := KindToAddress.Label(); got != "to address" {
		t.Fatalf("label %q", got)
	}
}

func TestMyNudgeIsNotAReviewersComment(t *testing.T) {
	p := pr("ana", "bia")
	evs := []dinv.Event{
		ev(dinv.KindPush, "h2", "ana", h(1), ""),
		requested("bia", "ana", h(1)),
		ev(dinv.KindNudged, "", "", h(60), "@bia"),
		ev(dinv.KindComment, "c1", "me", h(60).Add(2*time.Minute), ""),
	}
	if r := derive(p, h(61), evs...); r.Kind != KindReview || r.WaitingOn != "bia" {
		t.Fatalf("my nudge became a reviewer's comment: %+v", r)
	}
	later := append(evs, ev(dinv.KindComment, "c2", "me", h(62), ""))
	if r := derive(p, h(63), later...); r.Kind != KindReply {
		t.Fatalf("a later comment of mine still counts: %+v", r)
	}
	mine := pr("me", "bia")
	evs[0].Actor, evs[1].Actor = "me", "me"
	if r := derive(mine, h(61), evs...); r.Kind != KindReview || r.WaitingOn != "bia" {
		t.Fatalf("my nudge on my pull request became my reply: %+v", r)
	}
}

func TestTheNudgeCommentIsTheFirstOfMineAfterTheNudge(t *testing.T) {
	p := pr("ana", "bia")
	base := []dinv.Event{
		ev(dinv.KindPush, "h2", "ana", h(1), ""),
		requested("bia", "ana", h(1)),
		ev(dinv.KindNudged, "", "", h(60), "@bia"),
	}
	slow := append(slices.Clone(base), ev(dinv.KindComment, "c1", "me", h(60).Add(50*time.Minute), ""))
	if r := derive(p, h(62), slow...); r.Kind != KindReview || r.WaitingOn != "bia" {
		t.Fatalf("a nudge posted 50 minutes after it was asked became my reply: %+v", r)
	}
	answered := append(slices.Clone(base),
		ev(dinv.KindComment, "c0", "bia", h(60).Add(5*time.Minute), ""),
		ev(dinv.KindComment, "c1", "me", h(60).Add(10*time.Minute), ""))
	if r := derive(p, h(62), answered...); r.Kind != KindReply {
		t.Fatalf("my answer to bia after the nudge was taken for the nudge: %+v", r)
	}
	late := append(slices.Clone(base), ev(dinv.KindComment, "c1", "me", h(63), ""))
	if r := derive(p, h(64), late...); r.Kind != KindReply {
		t.Fatalf("a comment of mine hours after the nudge was taken for it: %+v", r)
	}
}

func TestKindLabel(t *testing.T) {
	for k, want := range map[Kind]string{KindHeldByCap: "held (cap)", KindRoundPending: "round soon", KindChanges: "changes asked", KindBlocked: "round blocked", Kind("new"): "new"} {
		if got := k.Label(); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
}

func deriveTurn(p dinv.PR, turn *TurnFacts, waiting bool) Result {
	return Derive(Input{PR: p, Viewer: "me", Now: h(10), Stalled: th, Turn: turn, SessionWaiting: waiting})
}

func TestTheTurnDrivesTheSituation(t *testing.T) {
	for _, c := range []struct {
		name    string
		turn    *TurnFacts
		waiting bool
		want    Kind
		reason  string
	}{
		{"started", &TurnFacts{Intent: "review", Role: "pr.review", State: "started", At: h(1)}, false, KindLoopWorking, ""},
		{"started on a permission prompt", &TurnFacts{Intent: "review", Role: "pr.review", State: "started", At: h(1)}, true, KindLoopWaiting, "waiting for a permission in the terminal"},
		{"asked", &TurnFacts{Intent: "review", Role: "pr.review", State: "asked", Summary: "keep the old column?", At: h(2)}, false, KindLoopWaiting, "keep the old column?"},
		{"asked and answered", &TurnFacts{Intent: "review", Role: "pr.review", State: "asked", Summary: "s", At: h(2), AnsweredAt: h(3)}, false, KindLoopWorking, "your answer was sent"},
		{"failed", &TurnFacts{Intent: "review", Role: "pr.review", State: "failed", Summary: "gh api 502", At: h(2)}, false, KindLoopStopped, "gh api 502"},
		{"unrecorded", &TurnFacts{Intent: "review", Role: "pr.review", State: "unrecorded", Summary: "Should I drop it?", At: h(2)}, false, KindLoopStopped, "the agent ended its turn without a record: Should I drop it?"},
		{"unrecorded with no message", &TurnFacts{Intent: "address", Role: "pr.address", State: "unrecorded", At: h(2)}, false, KindLoopStopped, "the agent ended its turn without a record"},
		{"done", &TurnFacts{Intent: "review", Role: "pr.review", State: "done", Summary: "pushed", At: h(2)}, false, KindNoReviewer, ""},
		{"sent", &TurnFacts{Intent: "review", Role: "pr.review", State: "sent", At: h(2)}, false, KindNoReviewer, ""},
		{"a nudge asks nothing", &TurnFacts{Intent: "nudge", State: "asked", Summary: "s", At: h(2)}, false, KindNoReviewer, ""},
	} {
		r := deriveTurn(pr("me"), c.turn, c.waiting)
		if r.Kind != c.want || (c.reason != "" && r.Reason != c.reason) {
			t.Errorf("%s: %+v", c.name, r)
		}
		if c.want != KindNoReviewer && (r.WaitingOn != "me" || !r.Since.Equal(c.turn.At)) {
			t.Errorf("%s: not waiting on me since the turn: %+v", c.name, r)
		}
	}
}

func TestTheTurnComesAfterTheHeldRound(t *testing.T) {
	r := Derive(Input{PR: pr("me"), Viewer: "me", Now: h(10), Stalled: th,
		Events: []dinv.Event{ev(dinv.KindRoundHeld, "", "me", h(9), "5 rounds today")},
		Turn:   &TurnFacts{Intent: "address", Role: "pr.address", State: "asked", Summary: "s", At: h(2)}})
	if r.Kind != KindHeldByCap {
		t.Fatalf("%+v", r)
	}
}

func TestACardInNeedsMeIsNotATurnsState(t *testing.T) {
	p := pr("me")
	p.Workspace = dinv.Workspace{ID: "ws1", Status: "needs-me", Comment: "triage asks: keep it?"}
	if r := deriveTurn(p, &TurnFacts{Intent: "address", Role: "pr.address", State: "done", Summary: "pushed", At: h(2)}, false); r.Kind == KindLoopWaiting {
		t.Fatalf("the card overrode "+brand.Name+"'s turn: %+v", r)
	}
	if r := deriveTurn(p, nil, false); r.Kind != KindNoReviewer {
		t.Fatalf("a pull request without a turn read its card: %+v", r)
	}
}

func TestOrderHoldsEveryLabeledKindOnce(t *testing.T) {
	if len(Order) != len(labels) {
		t.Fatalf("Order has %d kinds, labels %d", len(Order), len(labels))
	}
	seen := map[Kind]bool{}
	for _, k := range Order {
		if _, ok := labels[k]; !ok || seen[k] {
			t.Errorf("%s: labeled %v, twice %v", k, ok, seen[k])
		}
		seen[k] = true
	}
}

func TestRowKindEnumIsEveryKind(t *testing.T) {
	var kinds []string
	for _, k := range Order {
		kinds = append(kinds, string(k))
	}
	var row builtin.Fact
	for _, f := range builtin.ActionFacts {
		if f.Name == "row.kind" {
			row = f
		}
	}
	if got := row.Enum[unit.GitHub]; !slices.Equal(got, kinds) {
		t.Errorf("row.kind's GitHub enum is %q, want %q", got, kinds)
	}
}
