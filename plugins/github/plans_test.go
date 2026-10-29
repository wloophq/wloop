package github

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

// apiListing is a listing of acme/api's main checkout, so that acme owns
// the repositories automated rounds run for.
var apiListing = src.Listing{Repos: []src.Repo{{ID: "r1", Remote: "github.com/acme/api", Path: "/src/api", DefaultBranch: "main"}},
	Workspaces: []src.Workspace{{Manager: "orca", ID: "main", Repo: "acme/api", RepoID: "r1", Path: "/src/api", Branch: "main", IsMain: true, Local: true}}}

// requested stores a pull request of bruno that requests ana's review, read
// in full.
func requested(t *testing.T, p *Plugin) pr.Ref {
	t.Helper()
	ctx := context.Background()
	if err := p.store().SetViewer(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	if err := p.store().UpsertPR(ctx, pr.PR{Ref: ref7, Title: "Change 7", Author: "bruno", State: "OPEN", HeadOid: "h7", HeadBranch: "feat/7",
		BaseBranch: "main", ReviewRequests: []string{"ana"}, CreatedAt: testAt.Add(-48 * time.Hour), Reasons: []string{"requested"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.store().SaveDetail(ctx, ref7, store.Detail{Version: store.DetailVersion, ReadAt: testAt.Add(-time.Minute), AuthorAssociation: "MEMBER"}); err != nil {
		t.Fatal(err)
	}
	return ref7
}

func TestTheFirstRoundIsPlannedUntilItsSkipMarkIsInTheRoundState(t *testing.T) {
	h := &fakeHost{listing: apiListing}
	p := started(t, &githubGraphQL{}, h)
	ref := requested(t, p)
	ctx := context.Background()
	got, err := p.Plan(ctx, ref.Unit(), src.RoundState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rounds) != 1 || got.Rounds[0].Name != plan.RoundFirst || got.Rounds[0].NotDue != "" || got.Rounds[0].Head != "h7" ||
		!maps.Equal(got.Rounds[0].OnSkipped, map[string]string{plan.MarkFirstSkipped: "1"}) {
		t.Fatalf("plan %+v", got)
	}
	got, err = p.Plan(ctx, ref.Unit(), src.RoundState{Marks: map[string]string{plan.MarkFirstSkipped: "1"}})
	if err != nil || len(got.Rounds) != 0 {
		t.Fatalf("a skipped first round is planned: %+v %v", got, err)
	}
	if _, err := p.Plan(ctx, sdk.Unit{Provider: "slack", Key: "C1/1"}, src.RoundState{}); err == nil {
		t.Fatal("a Slack unit is planned")
	}
}

func TestTheReportCarriesThePlannersFactsAndRank(t *testing.T) {
	h := &fakeHost{listing: apiListing}
	p := started(t, &githubGraphQL{}, h)
	ref := requested(t, p)
	if err := p.Notify(context.Background(), []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicRounds}); err != nil {
		t.Fatal(err)
	}
	s := h.lastReport(t, ref)
	facts := map[string]src.Value{}
	for _, f := range s.Facts {
		facts[f.Name] = f.Value
	}
	if !facts["pr.first.due"].Bool || facts["pr.first.reason"].String != "my review was requested" || facts["pr.fingerprint"].String != "first:h7" {
		t.Fatalf("facts %+v", facts)
	}
	var d Data
	if err := json.Unmarshal(s.Data, &d); err != nil || d.Plan == nil || d.Plan.Rank.Path != 1 {
		t.Fatalf("plan data %+v %v", d.Plan, err)
	}
	h.mu.Lock()
	h.listing.Partial = true
	h.mu.Unlock()
	if err := p.Notify(context.Background(), nil, []sdk.Topic{sdk.TopicWorkspaces}); err != nil {
		t.Fatal(err)
	}
	for _, f := range h.lastReport(t, ref).Facts {
		if f.Name == "pr.first.due" && f.Value.Bool {
			t.Fatal("a partial listing leaves a first round due")
		}
	}
}

func TestPRReviewedAppliesOnTheLaunchRouteOnly(t *testing.T) {
	h := &fakeHost{listing: apiListing}
	p := started(t, &githubGraphQL{}, h)
	ctx := context.Background()
	if err := p.store().SetViewer(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	if err := p.store().UpsertPR(ctx, pr.PR{Ref: ref7, Author: "bruno", State: "OPEN", HeadOid: "h7", Reasons: []string{"reviewed"}}); err != nil {
		t.Fatal(err)
	}
	reviewed := testAt.Add(-time.Hour)
	if err := p.store().SaveDetail(ctx, ref7, store.Detail{Version: store.DetailVersion, ReadAt: testAt, AuthorAssociation: "MEMBER",
		Commits: []pr.Commit{{Oid: "h6", Parents: 1}, {Oid: "h7", Parents: 1}},
		Reviews: []pr.Review{{ID: "r1", State: pr.ReviewCommented, Author: "ana", SubmittedAt: reviewed, CommitOid: "h6"}}}); err != nil {
		t.Fatal(err)
	}
	apply := func(route string, taken bool) {
		t.Helper()
		err := p.ApplyEffect(ctx, src.EffectRequest{Effect: EffectReviewed, Unit: ref7.Unit(), Turn: &src.TurnRef{ID: "t1", Head: "h7"}, Route: route, Taken: taken})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []string{src.RouteSession, src.RouteAsked} {
		apply(route, true)
	}
	apply(src.RouteLaunch, false)
	if got := h.markSets(); len(got) != 0 {
		t.Fatalf("pr.reviewed off the launch route, or not taken, wrote %v", got)
	}
	apply(src.RouteLaunch, true)
	want := map[string]string{plan.MarkReviewed: reviewed.UTC().Format(time.RFC3339) + "@h7"}
	if got := h.markSets(); len(got) != 1 || !maps.Equal(got[0], want) {
		t.Fatalf("a launched review round wrote %v, want %v", got, want)
	}
}

func TestPRAddressedAndNudgedApplyOnAnyRoute(t *testing.T) {
	h := &fakeHost{}
	p := started(t, &githubGraphQL{}, h)
	ctx := context.Background()
	if err := p.ApplyEffect(ctx, src.EffectRequest{Effect: EffectAddressed, Unit: ref7.Unit(), Turn: &src.TurnRef{ID: "t1", Head: "h7"}, Route: src.RouteSession}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{plan.MarkAddressedReviews: testAt.UTC().Format(time.RFC3339), plan.MarkAddressedChecks: "h7", plan.MarkAddressedConflict: "h7"}
	if got := h.markSets(); len(got) != 1 || !maps.Equal(got[0], want) {
		t.Fatalf("pr.addressed wrote %v, want %v", got, want)
	}
	inputs := json.RawMessage(`{"who":["bruno","carla"]}`)
	if err := p.ApplyEffect(ctx, src.EffectRequest{Effect: nudge.EffectNudged, Unit: ref7.Unit(), Turn: &src.TurnRef{ID: "t2"}, Route: src.RouteLaunch, Inputs: inputs}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.events); n != 1 || h.events[0].Kind != pr.KindNudged || h.events[0].Key != "t2" || string(h.events[0].Data) != "bruno, carla" {
		t.Fatalf("events %+v", h.events)
	}
}

func TestAnAdoptionIsThePluginsAndForgetsTheAddressedReviews(t *testing.T) {
	h := &fakeHost{}
	p := started(t, &githubGraphQL{}, h)
	ctx := context.Background()
	basics, _ := json.Marshal(pr.PullRequest{Facts: pr.Facts{Ref: ref7, Title: "Change 7", Author: "bruno", State: "OPEN", HeadRefOid: "h7", HeadRefName: "feat/7"}})
	if _, err := p.Action(ctx, src.ActionRequest{Unit: ref7.Unit(), Action: ActionAdopt, Args: basics}); err != nil {
		t.Fatal(err)
	}
	if o, err := p.store().Override(ctx, ref7); err != nil || !o.Adopted {
		t.Fatalf("override %+v %v", o, err)
	}
	if got := h.reported(ref7); len(got) == 0 || !got[len(got)-1].Adopted || got[len(got)-1].PR.Title != "Change 7" {
		t.Fatalf("reported %+v", got)
	}
	if got := h.markSets(); len(got) != 1 || !maps.Equal(got[0], map[string]string{plan.MarkAddressedReviews: ""}) {
		t.Fatalf("marks %v", got)
	}
}

func TestThePayloadIsThePullRequestAnchoredToTheRecord(t *testing.T) {
	h := &fakeHost{}
	gql := &githubGraphQL{basics: `{"number":7,"title":"Add retries","state":"OPEN","headRefName":"feat/7","baseRefName":"main","headRefOid":"h7",
 "isCrossRepository":false,"authorAssociation":"MEMBER","repository":{"nameWithOwner":"acme/api","visibility":"PRIVATE"},"author":{"__typename":"User","login":"bruno"},
 "reviews":{"nodes":[{"state":"APPROVED","submittedAt":"2026-10-20T09:00:00Z","viewerDidAuthor":true,"commit":{"oid":"drifted"}}]},
 "reviewRequests":{"nodes":[]},"labels":{"nodes":[{"name":"backend"}]}}`}
	p := started(t, gql, h)
	v := &src.Verdict{Head: "h6", Endorsed: "yes: by hand", At: time.Date(2026, 10, 20, 9, 1, 0, 0, time.UTC)}
	got, err := p.Payload(context.Background(), src.PayloadRequest{Unit: ref7.Unit(), LastVerdict: v})
	if err != nil {
		t.Fatal(err)
	}
	if got.Arg != ref7.URL() || got.Wording != (src.Wording{Noun: "pull request", This: "this pull request", Ref: "acme/api#7", Short: "acme/api#7"}) {
		t.Fatalf("arg %q, wording %+v", got.Arg, got.Wording)
	}
	var pl Payload
	if err := json.Unmarshal(got.Data, &pl); err != nil {
		t.Fatal(err)
	}
	if pl.Title != "Add retries" || pl.Author != "bruno" || pl.Prior == nil || pl.Prior.Head != "h6" || pl.Prior.Endorsed != "yes: by hand" ||
		pl.Prior.Verdict != "APPROVED" || pl.LastReviewVerdict != "APPROVED" || len(pl.Labels) != 1 {
		t.Fatalf("payload %+v, prior %+v", pl, pl.Prior)
	}
	facts := map[string]src.Value{}
	for _, f := range got.Facts {
		facts[f.Name] = f.Value
	}
	if facts["pr.author"].String != "bruno" || facts["pr.slug"].String != "acme/api#7" || facts["pr.association"].String != "MEMBER" {
		t.Fatalf("facts %+v", facts)
	}
}

func TestTheWordingIsTheProfileLayers(t *testing.T) {
	raw, err := os.ReadFile("profile/" + wordingFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `noun = "pull request"`) {
		t.Fatalf("%s:\n%s", wordingFile, raw)
	}
}

func TestTheBriefRevisionIsTheStoredHeadEditAndFiles(t *testing.T) {
	p := started(t, &githubGraphQL{}, &fakeHost{})
	ctx := context.Background()
	edited := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if err := p.store().UpsertPR(ctx, pr.PR{Ref: ref7, State: "OPEN", HeadOid: "h9", BodyEditedAt: edited, ChangedFiles: 2}); err != nil {
		t.Fatal(err)
	}
	at, err := p.BriefRevision(ctx, ref7.Unit())
	if err != nil || at.Revision != "h9 2026-10-14T12:00:00.000000000Z" || string(at.Parts) != `{"changed_files":2}` || at.Attempt != "h9 2026-10-14T12:00:00Z 2" {
		t.Fatalf("%+v %s %v", at, at.Parts, err)
	}
	d, err := p.BriefDescribe(ctx, src.BriefInput{Bytes: 120, Cut: true, Parts: json.RawMessage(`{"changed_files":3}`)})
	if err != nil || d != "a pull request of 3 changed files, its diff 120 bytes, cut" {
		t.Fatalf("describe %q %v", d, err)
	}
	if want, err := p.BriefWant(ctx, ref7.Unit()); err != nil || want {
		t.Fatalf("want %v %v: the host's queue briefs a pull request", want, err)
	}
}

func TestTheBriefInputLineIsTheProfileLayersTemplate(t *testing.T) {
	h := &fakeHost{profile: map[string]string{wordingFile: "noun = \"change\"\nthis = \"this change\"\nbrief_input = \"{{.ChangedFiles}} files{{if .Cut}}, partial{{end}}\"\n"}}
	p := started(t, &githubGraphQL{}, h)
	ctx := context.Background()
	in := src.BriefInput{Bytes: 120, Cut: true, Parts: json.RawMessage(`{"changed_files":3}`)}
	if d, err := p.BriefDescribe(ctx, in); err != nil || d != "3 files, partial" {
		t.Fatalf("describe %q %v, want the user's template", d, err)
	}
	h.profile[wordingFile] = "noun = \"change\"\nthis = \"this change\"\n"
	if d, err := p.BriefDescribe(ctx, in); err == nil {
		t.Fatalf("describe %q with no brief_input, want an error", d)
	}
}

func TestBriefStaleIsTheQueuesRule(t *testing.T) {
	p := started(t, &githubGraphQL{}, &fakeHost{})
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	edited := now.Add(-time.Hour)
	base := pr.PR{HeadOid: "h1", BodyEditedAt: edited, ChangedFiles: 3}
	version := "b2"
	attempt := version + " " + revisionOf(base).Attempt
	for _, c := range []struct {
		name    string
		cur     func(*pr.PR)
		written src.BriefWritten
		stale   bool
	}{
		{"same", nil, src.BriefWritten{At: revisionOf(base), Version: version}, false},
		{"a new head alone", func(x *pr.PR) { x.HeadOid = "h2" }, src.BriefWritten{At: revisionOf(base), Version: version}, false},
		{"a body edited just now", func(x *pr.PR) { x.BodyEditedAt = now.Add(-time.Minute) }, src.BriefWritten{At: revisionOf(base), Version: version}, false},
		{"a body quiet since its edit", func(x *pr.PR) { x.BodyEditedAt = now.Add(-bodyQuiet) }, src.BriefWritten{At: revisionOf(base), Version: version}, true},
		{"a few new files", func(x *pr.PR) { x.ChangedFiles = 3 + refreshFiles }, src.BriefWritten{At: revisionOf(base), Version: version}, false},
		{"many new files", func(x *pr.PR) { x.ChangedFiles = 4 + refreshFiles }, src.BriefWritten{At: revisionOf(base), Version: version}, true},
		{"a failure inside its backoff", nil, src.BriefWritten{At: revisionOf(base), Error: "boom", Failures: 2, AttemptedAt: now.Add(-time.Hour), AttemptOf: attempt}, false},
		{"a failure past its backoff", nil, src.BriefWritten{At: revisionOf(base), Error: "boom", Failures: 1, AttemptedAt: now.Add(-2 * time.Hour), AttemptOf: attempt}, true},
		{"a failure of another attempt", func(x *pr.PR) { x.HeadOid = "h2" }, src.BriefWritten{At: revisionOf(base), Error: "boom", Failures: 2, AttemptedAt: now.Add(-time.Hour), AttemptOf: attempt}, true},
	} {
		cur := base
		if c.cur != nil {
			c.cur(&cur)
		}
		got, err := p.BriefStale(ctx, src.BriefStaleRequest{Written: c.written, Current: revisionOf(cur), Version: version, Now: now})
		if err != nil || got != c.stale {
			t.Errorf("%s: stale %v %v, want %v", c.name, got, err, c.stale)
		}
	}
}
