package prrow

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
)

// fixtureNow is the instant every fixture row is derived at.
var fixtureNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

const fixtureViewer = "me-login"

// rowCase is one pull request of the deriver fixtures with everything its
// row derives from.
type rowCase struct {
	name     string
	pr       dinv.PR
	events   []dinv.Event
	override dinv.Override
	mute     overlay.Mute
	turn     *TurnFact
	pending  *overlay.Pending
	brief    *api.BriefSummary
	intake   *api.Intake
	activity *turn.Activity
}

func ago(d time.Duration) time.Time { return fixtureNow.Add(-d) }

const day = 24 * time.Hour

// rowCases cover every situation kind, both roles, workspaces, briefs,
// intakes, overrides and pending rounds.
func rowCases() []rowCase {
	ref := func(n int) pr.Ref { return pr.Ref{Owner: "acme", Repo: "api", Number: n} }
	open := func(n int, author string, edit func(*dinv.PR)) dinv.PR {
		p := dinv.PR{Ref: ref(n), Title: "change " + author, Author: author, State: "OPEN", HeadOid: "h1", HeadBranch: "feat/x", BaseBranch: "main",
			CreatedAt: ago(time.Duration(n) * 13 * time.Hour), Reasons: []string{"involved"}, ChecksState: "SUCCESS", ChangedFiles: n}
		if edit != nil {
			edit(&p)
		}
		return p
	}
	ev := func(n int, kind, actor string, at time.Time, data string) dinv.Event {
		return dinv.Event{PR: ref(n), Kind: kind, Key: kind + at.Format(time.RFC3339), Actor: actor, At: at, Data: data}
	}
	reviewWS := dinv.Workspace{ID: "ws-r", DisplayName: "review", Status: "in-review", IsReview: true}
	authorWS := dinv.Workspace{ID: "ws-a", DisplayName: "author", Status: "in-progress"}
	return []rowCase{
		{name: "review asked of me", pr: open(1, "ana", func(p *dinv.PR) { p.ReviewRequests = []string{fixtureViewer} }),
			events: []dinv.Event{ev(1, dinv.KindReviewRequest, "ana", ago(3*day), fixtureViewer)},
			brief:  &api.BriefSummary{Kind: "feature", Size: "m", Risk: "high", Goal: "add x", Tags: []string{"lang:go", "area:api"}}},
		{name: "review asked of a team", pr: open(2, "bob", func(p *dinv.PR) { p.ReviewRequests = []string{"team:acme/core", "carol"} }),
			brief: &api.BriefSummary{Kind: "fix", Size: "xs", Risk: "low"}},
		{name: "review asked of another", pr: open(3, "bob", func(p *dinv.PR) { p.ReviewRequests = []string{"carol"} })},
		{name: "mine without reviewer", pr: open(4, fixtureViewer, nil)},
		{name: "mine draft", pr: open(5, fixtureViewer, func(p *dinv.PR) { p.IsDraft = true; p.Workspace = authorWS })},
		{name: "mine checks failing", pr: open(6, fixtureViewer, func(p *dinv.PR) {
			p.ChecksState = "FAILURE"
			p.FailingChecks = []dinv.FailingCheck{{Name: "lint"}, {Name: "unit"}}
		})},
		{name: "mine changes asked", pr: open(7, fixtureViewer, nil),
			events: []dinv.Event{ev(7, dinv.KindReview, "carol", ago(9*day), "CHANGES_REQUESTED:h1")}},
		{name: "mine can merge", pr: open(8, fixtureViewer, func(p *dinv.PR) { p.ReviewDecision = "APPROVED"; p.Workspace = authorWS }),
			events: []dinv.Event{ev(8, dinv.KindReview, "carol", ago(2*time.Hour), "APPROVED:h1")}},
		{name: "mine conflicts", pr: open(9, fixtureViewer, func(p *dinv.PR) {
			p.ReviewDecision = "APPROVED"
			p.Mergeable = "CONFLICTING"
			p.MergeableAt = ago(time.Hour)
		})},
		{name: "reply due on theirs", pr: open(10, "ana", nil),
			events: []dinv.Event{ev(10, dinv.KindComment, fixtureViewer, ago(5*day), "")}},
		{name: "reviewer back", pr: open(11, "ana", nil),
			events: []dinv.Event{ev(11, dinv.KindComment, fixtureViewer, ago(6*day), ""), ev(11, dinv.KindComment, "ana", ago(5*day), "")}},
		{name: "reviewer back from another", pr: open(12, fixtureViewer, nil),
			events: []dinv.Event{ev(12, dinv.KindComment, "dave", ago(3*day), ""), ev(12, dinv.KindComment, fixtureViewer, ago(2*day), "")}},
		{name: "merged", pr: open(13, "ana", func(p *dinv.PR) { p.State = "MERGED"; p.MergedAt = ago(day); p.Workspace = reviewWS }),
			events: []dinv.Event{ev(13, dinv.KindMerged, "ana", ago(day), "")}},
		{name: "closed without workspace", pr: open(14, "ana", func(p *dinv.PR) { p.State = "CLOSED"; p.ClosedAt = ago(day) })},
		{name: "muted for a while", pr: open(15, "ana", func(p *dinv.PR) { p.ReviewRequests = []string{fixtureViewer} }),
			mute:   overlay.Mute{Until: fixtureNow.Add(5 * time.Hour)},
			events: []dinv.Event{ev(15, dinv.KindMuted, fixtureViewer, ago(time.Hour), "")}},
		{name: "muted until state change", pr: open(16, "ana", func(p *dinv.PR) { p.ReviewRequests = []string{fixtureViewer} }),
			mute: overlay.Mute{UntilStateChange: true}},
		{name: "mute spent", pr: open(17, "ana", nil)},
		{name: "held by the cap", pr: open(18, "ana", func(p *dinv.PR) { p.Workspace = reviewWS }),
			events: []dinv.Event{ev(18, dinv.KindRoundHeld, "", ago(time.Hour), "cap reached")}},
		{name: "round blocked", pr: open(19, "ana", nil),
			events: []dinv.Event{ev(19, dinv.KindRoundBlocked, fixtureViewer, ago(2*time.Hour), "no access")}},
		{name: "to address", pr: open(20, fixtureViewer, nil),
			events: []dinv.Event{ev(20, dinv.KindWouldAddress, "", ago(3*time.Hour), "carol asked")}},
		{name: "round pending", pr: open(21, "ana", func(p *dinv.PR) { p.ReviewRequests = []string{fixtureViewer}; p.Workspace = reviewWS }),
			pending: &overlay.Pending{Intent: "review", Reason: "new push", Since: ago(time.Minute), Deadline: fixtureNow.Add(4 * time.Minute)}},
		{name: "agent asks", pr: open(22, "ana", func(p *dinv.PR) { p.Workspace = reviewWS }),
			turn: &TurnFact{Facts: overlay.Turn{Intent: "review", Role: "pr.review", State: "asked", Summary: "which branch?", At: ago(30 * time.Minute)}}},
		{name: "agent working", pr: open(23, "ana", func(p *dinv.PR) { p.Workspace = reviewWS; p.Reasons = nil }),
			turn: &TurnFact{Facts: overlay.Turn{Intent: "review", Role: "pr.review", State: "started", At: ago(10 * time.Minute)}, Session: "s1"}},
		{name: "agent stopped", pr: open(24, fixtureViewer, func(p *dinv.PR) { p.Workspace = authorWS }),
			turn: &TurnFact{Facts: overlay.Turn{Intent: "address", Role: "pr.address", State: "failed", Summary: "tests broke", At: ago(day)}}},
		{name: "workspace agent waiting", pr: open(25, "ana", func(p *dinv.PR) {
			p.Workspace = reviewWS
			p.Workspace.AgentStates = []turn.AgentState{turn.AgentWaiting}
		})},
		{name: "adopted outside scope", pr: open(26, "ana", func(p *dinv.PR) { p.Reasons = nil; p.ReviewRequests = []string{"carol"} }),
			override: dinv.Override{Adopted: true, Hold: true}},
		{name: "tracked", pr: open(27, "bob", func(p *dinv.PR) { p.Reasons = []string{"tracked"} }),
			override: dinv.Override{Tracked: true, NextGuidance: "look at x"}},
		{name: "approved review head", pr: open(28, "ana", func(p *dinv.PR) { p.Workspace = reviewWS; p.ReviewRequests = []string{"carol"} }),
			events: []dinv.Event{ev(28, dinv.KindReview, fixtureViewer, ago(day), "APPROVED:h1")}},
		{name: "intake briefing", pr: open(29, "ana", func(p *dinv.PR) { p.ReviewRequests = []string{fixtureViewer} }),
			intake: &api.Intake{Seq: 3, PR: "acme/api#29", Intent: "review", State: api.IntakeBriefing, Attempt: 2, LastError: "timeout"}},
		{name: "no created at, pending checks", pr: open(30, "erin", func(p *dinv.PR) {
			p.CreatedAt = time.Time{}
			p.ChecksState = "PENDING"
			p.ReviewRequests = []string{"team:acme/web"}
			p.Ref.Repo = "web"
		}), activity: &turn.Activity{Turns: 3, OpenTurns: 1, LastIntent: "review", Tokens: 1200, CostUSD: 0.5, BriefRetryAt: fixtureNow.Add(2 * time.Hour)}},
		{name: "old stalled review", pr: open(31, "ana", func(p *dinv.PR) {
			p.ReviewRequests = []string{fixtureViewer}
			p.CreatedAt = ago(60 * day)
		}), events: []dinv.Event{ev(31, dinv.KindOpened, "ana", ago(60*day), "")},
			brief: &api.BriefSummary{Kind: "chore", Size: "xl", Risk: "medium", OpenQuestions: 2, Tags: []string{"lang:ts"}}},
	}
}

// fixtureDeriver derives the fixture rows: the bundled intents, my teams
// and the stalled thresholds of the default config.
func fixtureDeriver(t *testing.T, at time.Time, cases []rowCase) Deriver {
	t.Helper()
	l := bundled(t)
	d := Deriver{Viewer: fixtureViewer, Teams: []string{"acme/core"}, Now: at, Stalled: config.Defaults().GitHub.Stalled, Loaded: l, Cfg: config.Defaults(),
		GateOn: true, BriefOn: true, Plans: map[unit.Unit]overlay.Pending{}, Turns: map[pr.Ref]TurnFact{},
		Mutes: map[unit.Unit]overlay.Mute{}}
	for _, c := range cases {
		d.Mutes[c.pr.Ref.Unit()] = c.mute
		if c.pending != nil {
			d.Plans[c.pr.Ref.Unit()] = *c.pending
		}
		if c.turn != nil {
			d.Turns[c.pr.Ref] = *c.turn
		}
	}
	return d
}

// report is what the GitHub plugin reports of the case's pull request.
func (c rowCase) report(t *testing.T) Report {
	t.Helper()
	in := ghreporttest.Input{PR: c.pr, Events: c.events, Adopted: c.override.Adopted, Tracked: c.override.Tracked}
	if c.turn != nil && c.turn.Facts.Role == "pr.address" {
		in.AddressedAt = c.turn.Facts.At
	}
	return reportOf(t, in)
}

// reportOf is what the GitHub plugin reports of in, derived by the
// plugin's code with the fixtures' viewer and teams at fixtureNow.
func reportOf(t *testing.T, in ghreporttest.Input) Report {
	t.Helper()
	s := ghreporttest.Snapshot(ghreporttest.Env{Viewer: fixtureViewer, Teams: []string{"acme/core"}, Now: fixtureNow, Loop: brand.Name}, in)
	rep, ok, err := ReportOf(s)
	if err != nil || !ok {
		t.Fatalf("report of %s: %v %v", in.PR.Ref, ok, err)
	}
	return rep
}
