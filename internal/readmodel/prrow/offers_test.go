package prrow

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/situation"
)

func TestOnlyMyOwnPullRequestWithNoReviewerAsksTheMaintainers(t *testing.T) {
	for _, c := range []struct {
		row  nudgeCase
		want []string
	}{
		{nudgeCase{Kind: situation.KindNoReviewer, Author: "Me", WaitingOn: "author"}, []string{NudgeMaintainers}},
		{nudgeCase{Kind: situation.KindNoReviewer, Author: "ana", WaitingOn: "author"}, []string{"@ana"}},
		{nudgeCase{Kind: situation.KindReply, Author: "me", WaitingOn: "author"}, nil},
		{nudgeCase{Kind: situation.KindNoReviewer, Author: "bia", WaitingOn: "author"}, []string{"@bia"}},
	} {
		if got, _ := c.row.targets("me", nil); !slices.Equal(got, c.want) {
			t.Errorf("%+v: %v, want %v", c.row, got, c.want)
		}
	}
	if got, _ := (nudgeCase{Kind: situation.KindNoReviewer, Author: "me", WaitingOn: "author"}).targets("", nil); slices.Equal(got, []string{NudgeMaintainers}) {
		t.Error("an unknown viewer asked the maintainers")
	}
}

func TestNudgeMentionsATeamOnlyWithoutPeople(t *testing.T) {
	r := nudgeCase{Kind: situation.KindReview, Author: "bia", WaitingOn: "ana, team:acme/backend"}
	for waiting, want := range map[string][]string{
		"ana, team:acme/backend": {"@ana"},
		"team:acme/backend":      {"@acme/backend"},
		"me, team:acme/backend":  {"@acme/backend"},
		"ana, carla":             {"@ana", "@carla"},
		"author":                 {"@bia"},
	} {
		r.WaitingOn = waiting
		if got, _ := r.targets("me", nil); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", waiting, got, want)
		}
	}
	r.WaitingOn, r.Author = "author", "ME"
	if got, why := r.targets("me", nil); got != nil || !strings.Contains(why, "waits on you") {
		t.Errorf("my own pull request: %v %q", got, why)
	}
}

func TestNudgeNeverMentionsABot(t *testing.T) {
	for _, c := range []struct {
		row    nudgeCase
		ignore []string
		why    string
	}{
		{nudgeCase{Kind: situation.KindNoReviewer, Author: "dependabot[bot]", WaitingOn: "author", Reason: "no reviewer requested"}, nil, "waits on dependabot[bot], a bot"},
		{nudgeCase{Kind: situation.KindDraft, Author: "Renovate", WaitingOn: "author", Reason: "draft"}, []string{"renovate"}, "waits on Renovate, which automated_reviewers or address_skip_reviewers lists"},
		{nudgeCase{Kind: situation.KindReply, Author: "", WaitingOn: "author", Reason: "x commented, no reply"}, nil, "waits on an author GitHub no longer knows"},
	} {
		got, why := c.row.targets("me", c.ignore)
		if got != nil || !strings.Contains(why, c.why) {
			t.Errorf("%+v: %v %q, want %q", c.row, got, why, c.why)
		}
	}
}

type nudgeCase struct {
	Kind                      situation.Kind
	Author, WaitingOn, Reason string
}

func (c nudgeCase) targets(viewer string, ignore []string) ([]string, string) {
	return NudgeTargets(api.Row{Kind: api.Kind(c.Kind), Summary: api.Summary{WaitsOn: c.WaitingOn, Reason: c.Reason}}, c.Author, viewer, ignore)
}

func TestANudgeTurnThatNeverStartedDoesNotHoldTheNextNudge(t *testing.T) {
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	sent := time.Date(2026, 10, 26, 13, 35, 0, 0, time.UTC)
	now := sent.Add(time.Hour)
	evs := []dinv.Event{{PR: ref, Kind: dinv.KindNudged, Key: "t1", At: sent, Data: "@ana"}}
	for _, c := range []struct {
		name    string
		turn    turn.Turn
		refused bool
	}{
		{"still running", turn.Turn{ID: "t1", State: turn.Sent}, true},
		{"unrecorded, never started", turn.Turn{ID: "t1", State: turn.Unrecorded}, false},
		{"dismissed before it started", turn.Turn{ID: "t1", State: turn.Dismissed}, false},
		{"replaced before it started", turn.Turn{ID: "t1", State: turn.Replaced}, false},
		{"unrecorded after it started", turn.Turn{ID: "t1", State: turn.Unrecorded, StartedAt: sent}, true},
		{"failed after it started", turn.Turn{ID: "t1", State: turn.Failed, StartedAt: sent}, true},
		{"done", turn.Turn{ID: "t1", State: turn.Done, StartedAt: sent}, true},
		{"another turn never started", turn.Turn{ID: "t2", State: turn.Unrecorded}, true},
	} {
		unstarted := Unstarted(turn.Book{Turns: []turn.Turn{c.turn}})
		if got := NudgeRefusal(ref, []string{"@ana"}, "", sent.Add(-time.Hour), evs, unstarted, now); (got != "") != c.refused {
			t.Errorf("%s: refusal %q, want refused %v", c.name, got, c.refused)
		}
	}
}
