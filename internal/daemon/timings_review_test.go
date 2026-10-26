package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
)

var viewer = me{login: "me", teams: []string{"acme/search"}}

func TestTheReviewWaitStartsAtTheRequestMadeOfMe(t *testing.T) {
	t.Parallel()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	opened := t0.Add(-2 * 24 * time.Hour)
	ev := func(kind, actor, data string, at time.Time) dinv.Event {
		return dinv.Event{PR: ref, Kind: kind, Actor: actor, Data: data, At: at}
	}
	d := dinv.TimingsData{PRs: []dinv.PR{{Ref: ref, Author: "ana", CreatedAt: opened}}, Events: map[github.Ref][]dinv.Event{ref: {
		ev(dinv.KindReviewRequest, "ana", "team:acme/web", opened.Add(time.Hour)),
		ev(dinv.KindReviewRequest, "ana", "bia", opened.Add(2*time.Hour)),
		ev(dinv.KindReviewRequest, "ana", "me", opened.Add(3*time.Hour)),
		ev(dinv.KindReview, "me", "COMMENTED:c1", opened.Add(5*time.Hour)),
	}}}
	if q := timings(d, t0.Add(-7*24*time.Hour), t0, viewer, nil).Milestones[api.MilestoneReviewWait]; q.Count != 1 || q.P50MS != (2*time.Hour).Milliseconds() {
		t.Fatalf("from my request: %+v", q)
	}
	d.Events[ref] = append(d.Events[ref][:2], ev(dinv.KindReviewRequest, "ana", "team:acme/search", opened.Add(4*time.Hour)), d.Events[ref][3])
	if q := timings(d, t0.Add(-7*24*time.Hour), t0, viewer, nil).Milestones[api.MilestoneReviewWait]; q.Count != 1 || q.P50MS != time.Hour.Milliseconds() {
		t.Fatalf("from my team's request: %+v", q)
	}
	d.Events[ref] = []dinv.Event{ev(dinv.KindReviewRequest, "ana", "bia", opened.Add(time.Hour)), ev(dinv.KindReview, "me", "COMMENTED:c1", opened.Add(5*time.Hour))}
	if q := timings(d, t0.Add(-7*24*time.Hour), t0, viewer, nil).Milestones[api.MilestoneReviewWait]; q.Count != 0 {
		t.Fatalf("never asked of me: %+v", q)
	}
}

func TestMigrationSeedsAreNotTimeInLoop(t *testing.T) {
	t.Parallel()
	recorded := t0.Add(-5 * 24 * time.Hour)
	seeded := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	live := github.Ref{Owner: "acme", Repo: "api", Number: 2}
	d := dinv.TimingsData{RecordedSince: recorded, PRs: []dinv.PR{{Ref: seeded}, {Ref: live}}, Events: map[github.Ref][]dinv.Event{
		seeded: {{PR: seeded, Kind: dinv.KindFollowed, At: recorded}, {PR: seeded, Kind: dinv.KindUnfollowed, At: recorded}},
		live:   {{PR: live, Kind: dinv.KindFollowed, At: recorded.Add(time.Hour)}, {PR: live, Kind: dinv.KindUnfollowed, At: recorded.Add(4 * time.Hour)}},
	}}
	if q := timings(d, t0.Add(-7*24*time.Hour), t0, viewer, nil).Milestones[api.MilestoneInLoop]; q.Count != 1 || q.P50MS != (3*time.Hour).Milliseconds() {
		t.Fatalf("%+v", q)
	}
}

func TestTheAuthorOfMyPullRequestIsMe(t *testing.T) {
	t.Parallel()
	mine := github.Ref{Owner: "acme", Repo: "api", Number: 9}
	theirs := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	since := t0.Add(-24 * time.Hour)
	d := dinv.TimingsData{PRs: []dinv.PR{{Ref: mine, Author: "me"}, {Ref: theirs, Author: "ana"}}, Events: map[github.Ref][]dinv.Event{
		mine:   {{PR: mine, Kind: dinv.KindSituation, Data: "merge:author", At: since.Add(time.Hour)}},
		theirs: {{PR: theirs, Kind: dinv.KindSituation, Data: "changes:author", At: since.Add(time.Hour)}},
	}}
	w := timings(d, since, t0, viewer, nil).Waiting
	if w[api.WaitingOnMe].PRs != 1 || w[api.WaitingOnOthers].PRs != 1 || w[api.WaitingOnMe].TotalMS != (23*time.Hour).Milliseconds() {
		t.Fatalf("%+v", w)
	}
}

func TestRunsBeforeRecordingStayOutOfTheFigures(t *testing.T) {
	t.Parallel()
	recorded := t0.Add(-24 * time.Hour)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	cost := 0.52
	call := int64(12000)
	d := dinv.TimingsData{RecordedSince: recorded, PRs: []dinv.PR{{Ref: ref, MergedAt: t0}},
		Runs: []dinv.BriefRun{
			{PR: ref, Trigger: "background", Outcome: "ok", StartedAt: recorded.Add(-48 * time.Hour), CostUSD: &cost},
			{PR: ref, Trigger: "background", Outcome: "ok", StartedAt: recorded.Add(-time.Hour), CostUSD: &cost, CallMS: &call},
		}}
	rep := timings(d, t0.Add(-30*24*time.Hour), t0, viewer, nil)
	if rep.Briefs.Unrecorded != 2 || rep.Briefs.CostUSD != 0 || rep.Briefs.Call.Count != 0 || rep.PRs[0].Briefs != 0 {
		t.Fatalf("briefs %+v prs %+v", rep.Briefs, rep.PRs)
	}
}

func TestChangeFailureWaitsForTheWindowAndIgnoresSharedFiles(t *testing.T) {
	t.Parallel()
	pr := func(n int, created time.Time) dinv.PR {
		return dinv.PR{Ref: github.Ref{Owner: "acme", Repo: "api", Number: n}, CreatedAt: created}
	}
	old, recent := t0.Add(-20*24*time.Hour), t0.Add(-3*24*time.Hour)
	a, b, fixA, fixB := pr(1, old.Add(-time.Hour)), pr(2, recent.Add(-time.Hour)), pr(3, old.Add(time.Hour)), pr(4, recent.Add(time.Hour))
	d := dinv.TimingsData{
		PRs: []dinv.PR{a, b, fixA, fixB},
		Events: map[github.Ref][]dinv.Event{
			a.Ref: {{PR: a.Ref, Kind: dinv.KindMerged, At: old}},
			b.Ref: {{PR: b.Ref, Kind: dinv.KindMerged, At: recent}},
		},
		PRTags:  map[github.Ref][]string{fixA.Ref: {"change:fix"}, fixB.Ref: {"change:fix"}},
		PRFiles: map[github.Ref][]string{a.Ref: {"go.mod", "go.sum", "CHANGELOG.md", "api/a.go"}, b.Ref: {"api/b.go"}, fixA.Ref: {"go.mod", "CHANGELOG.md"}, fixB.Ref: {"api/b.go"}},
	}
	cf := timings(d, t0.Add(-30*24*time.Hour), t0, viewer, nil).ChangeFailure
	if cf.Merged != 2 || cf.Briefed != 1 || cf.Recent != 1 || cf.Failed != 0 {
		t.Fatalf("%+v", cf)
	}
	d.PRFiles[fixA.Ref] = append(d.PRFiles[fixA.Ref], "api/a.go")
	if cf := timings(d, t0.Add(-30*24*time.Hour), t0, viewer, nil).ChangeFailure; cf.Failed != 1 {
		t.Fatalf("%+v", cf)
	}
}

func TestTheBriefWaitOfAnAutomatedFirstTurn(t *testing.T) {
	t.Parallel()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	byHand := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	wanted := t0.Add(-time.Hour)
	d := dinv.TimingsData{PRs: []dinv.PR{{Ref: ref}, {Ref: byHand}},
		Events: map[github.Ref][]dinv.Event{
			ref:    {{PR: ref, Kind: dinv.KindBriefWanted, At: wanted}},
			byHand: {{PR: byHand, Kind: dinv.KindBriefWanted, At: wanted}},
		},
		Turns: []dinv.TimedTurn{
			{Seq: 1, PR: ref, Intent: "review", Trigger: "automated", State: "sent", SentAt: wanted.Add(19 * time.Second)},
			{Seq: 2, PR: byHand, Intent: "review", Trigger: "by_hand", State: "sent", SentAt: wanted.Add(time.Minute)},
		}}
	if q := timings(d, t0.Add(-24*time.Hour), t0, viewer, nil).Turns[api.TurnBriefWait]; q.Count != 1 || q.P50MS != 19000 {
		t.Fatalf("%+v", q)
	}
}

func TestTheTurnListSkipsProbesAndCarriesEachDuration(t *testing.T) {
	t.Parallel()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	probe := github.Ref{Owner: ProbeOwner, Repo: ProbeRepo, Number: 1}
	d := dinv.TimingsData{PRs: []dinv.PR{{Ref: ref}, {Ref: probe}},
		Turns: []dinv.TimedTurn{
			{Seq: 1, PR: ref, Intent: "review", Trigger: "automated", State: "done", Agent: "claude", SentAt: t0, StartedAt: t0.Add(5 * time.Second), EndedAt: t0.Add(35 * time.Second)},
			{Seq: 2, PR: probe, Intent: "probe", State: "done", SentAt: t0, EndedAt: t0.Add(time.Minute)},
		},
		Tokens: []dinv.TurnTokens{{TurnSeq: 1, Model: "opus", Output: 900}, {TurnSeq: 2, Model: "opus", Output: 5}},
	}
	rep := timings(d, t0.Add(-time.Hour), t0.Add(time.Hour), viewer, nil)
	if len(rep.TurnList) != 1 || rep.TurnList[0].DurationMS != 30000 || rep.TurnList[0].OutputTokens != 900 || rep.TurnList[0].Agent != "claude" {
		t.Fatalf("turns %+v", rep.TurnList)
	}
	if !slices.Equal(prsOf(rep), []string{ref.URL()}) || rep.PRs[0].Turns != 1 {
		t.Fatalf("prs %+v", rep.PRs)
	}
	if _, ok := rep.Tokens["claude opus"]; !ok || len(rep.Tokens) != 1 {
		t.Fatalf("tokens %+v", rep.Tokens)
	}
}

func prsOf(rep api.TimingsReport) []string {
	var out []string
	for _, p := range rep.PRs {
		out = append(out, p.PR)
	}
	return out
}

func TestARequestOnAPullRequestWithNoTurnTakesTheSweeps(t *testing.T) {
	t.Parallel()
	books := turnstest.NewStore()
	books.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), Role: "pr.review", ID: "by-sweep", Trigger: turn.Automated, State: turn.Sent, SentAt: t0}}})
	s, _ := newIntakeServer(t, nil, func(d *Deps) { d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books} })
	raw, _ := json.Marshal(actionPayload{Action: "review", AfterTurn: 0})
	fresh := intake.Intake{Seq: 2, Unit: r7.Unit(), Intent: turn.Intent(dintent.Review), Request: raw}
	if _, ok, err := s.sentWhileWaiting(context.Background(), fresh); err != nil || !ok {
		t.Fatalf("a request on a pull request with no turn missed the sweep's: %v %v", ok, err)
	}
}
