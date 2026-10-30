package nudge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/publish"
	"github.com/wloophq/wloop/plugins/github/row"
	"github.com/wloophq/wloop/plugins/github/situation"
)

const (
	viewer   = "octocat"
	other    = "alice"
	reviewer = "bob"
	day      = 24 * time.Hour
)

var now = time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)

func ref(n int) pr.Ref { return pr.Ref{Owner: "acme", Repo: "api", Number: n} }

func open(n int, author string) pr.PR {
	return pr.PR{Ref: ref(n), Title: fmt.Sprintf("Change %d", n), Author: author, State: "OPEN", HeadOid: "h1", HeadBranch: "feat/x",
		BaseBranch: "main", URL: ref(n).URL(), CreatedAt: now.Add(-2 * day), Reasons: []string{"involved"}, ChecksState: "SUCCESS"}
}

func ev(n int, kind, actor string, at time.Time, data string) pr.Event {
	return pr.Event{PR: ref(n), Kind: kind, Key: kind + at.Format(time.RFC3339), Actor: actor, At: at, Data: data}
}

// pull is one pull request with what its nudge reads.
type pull struct {
	pr     pr.PR
	events []pr.Event
	host   nudge.Host
	// at is when it is derived; zero is now.
	at time.Time
}

func (s pull) when() time.Time {
	if s.at.IsZero() {
		return now
	}
	return s.at
}

func (s pull) row() row.Row {
	return row.Derive(row.Input{PR: s.pr, Events: s.events, Viewer: viewer, Teams: []string{}, Loop: "the loop", Now: s.when(),
		Stalled: situation.Thresholds{Review: 48 * time.Hour, Reply: 24 * time.Hour, Merge: 24 * time.Hour}})
}

func (s pull) propose() (src.Draft, bool) {
	d, ok, err := nudge.Propose(s.row(), s.host, profileTexts(), nil, s.when())
	if err != nil {
		panic(err)
	}
	return d, ok
}

// profileTexts are the nudge's texts in the plugin's profile layer.
func profileTexts() nudge.Texts {
	raw, err := os.ReadFile(filepath.Join("..", "profile", filepath.FromSlash(nudge.TextsFile)))
	if err != nil {
		panic(err)
	}
	x, err := nudge.ParseTexts(string(raw))
	if err != nil {
		panic(err)
	}
	return x
}

// stalledChanges is the viewer's changes requested on alice's pull request
// 1, eight days ago: it waits on alice and is stalled.
func stalledChanges() pull {
	s := pull{pr: open(1, other)}
	s.pr.CreatedAt = now.Add(-10 * day)
	s.events = []pr.Event{ev(1, pr.KindReview, viewer, now.Add(-8*day), "CHANGES_REQUESTED:h1")}
	return s
}

// stalledReview is the viewer's pull request 2 waiting on bob's review for
// ten days.
func stalledReview() pull {
	s := pull{pr: open(2, viewer)}
	s.pr.CreatedAt, s.pr.ReviewRequests = now.Add(-10*day), []string{reviewer}
	s.events = []pr.Event{ev(2, pr.KindReviewRequest, viewer, now.Add(-10*day), reviewer)}
	return s
}

// stalledReviewerBack is the viewer's pull request 3: bob commented five
// days ago, the viewer replied four days ago, and bob has not returned.
func stalledReviewerBack() pull {
	s := pull{pr: open(3, viewer)}
	s.pr.CreatedAt, s.pr.ReviewRequests = now.Add(-10*day), []string{reviewer}
	s.events = []pr.Event{
		ev(3, pr.KindReviewRequest, viewer, now.Add(-10*day), reviewer),
		ev(3, pr.KindComment, reviewer, now.Add(-5*day), ""),
		ev(3, pr.KindComment, viewer, now.Add(-4*day), ""),
	}
	return s
}

var githubMentionRE = regexp.MustCompile(`@\{github:([^{}\s]+)\}`)

// proposedWho is whom a proposal's text asks, as the nudged event records it.
func proposedWho(d src.Draft) string {
	return nudge.Who(githubMentionRE.ReplaceAllString(d.Text, "@$1"))
}

func TestNoNudgeForCardOnlyStates(t *testing.T) {
	for name, s := range map[string]pull{"changes": stalledChanges(), "review": stalledReview()} {
		d, ok := s.propose()
		if !ok {
			t.Fatalf("%s: no nudge for a stalled pull request (%s)", name, s.row().Kind())
		}
		if d.Text == "" || d.Purpose != nudge.Purpose || d.Unit != s.pr.Ref.Unit() {
			t.Fatalf("%s: %+v", name, d)
		}
		if want := (src.DraftOrigin{Kind: "auto", Rule: nudge.Rule, Key: "github:" + s.pr.Ref.String()}); d.Origin != want ||
			!slices.Equal(d.Effects, []string{nudge.EffectNudged, nudge.EffectTaskNudged}) || d.Delivery != "" || d.Kind != "" {
			t.Fatalf("%s: origin %+v effects %v, kind %q delivery %q", name, d.Origin, d.Effects, d.Kind, d.Delivery)
		}
	}
	draftPR := stalledReview()
	draftPR.pr.IsDraft = true
	hold := stalledReview()
	hold.host.Overrides = map[string]string{"hold": "true"}
	muted := stalledReview()
	muted.host.Asleep = true
	approved := stalledChanges()
	approved.pr.Workspace, approved.pr.ReviewRequests = pr.Workspace{ID: "ws-r", DisplayName: "review", Status: "in-review", IsReview: true}, []string{reviewer}
	approved.events = append(approved.events,
		ev(1, pr.KindReviewRequest, other, now.Add(-9*day), reviewer),
		ev(1, pr.KindReview, viewer, now.Add(-7*day), "APPROVED:h1"))
	working := stalledReview()
	working.pr.Workspace = pr.Workspace{ID: "ws-r", DisplayName: "review", Status: "in-review", IsReview: true, AgentStates: []string{"working"}}
	turn := stalledReview()
	turn.host.Book = src.Book{Unit: turn.pr.Ref.Unit(), Turns: []src.Turn{{Seq: 1, ID: "t1", Role: "pr.address", State: "started", StartedAt: now.Add(-time.Hour)}}}
	outOfScope := stalledReview()
	outOfScope.pr.Reasons = nil
	for name, s := range map[string]pull{"draft": draftPR, "hold": hold, "muted": muted,
		"approved": approved, "loop working": working, "loop turn": turn, "out of scope": outOfScope} {
		if d, ok := s.propose(); ok {
			r := s.row()
			t.Errorf("%s (%s, views %v): proposed %q", name, r.Kind(), r.Views, d.Text)
		}
	}
	ended := stalledReview()
	ended.host.Book = src.Book{Unit: ended.pr.Ref.Unit(), Turns: []src.Turn{{Seq: 1, ID: "t1", Role: "pr.address", State: "done", StartedAt: now.Add(-time.Hour)}}}
	for name, s := range map[string]pull{"ended turn": ended} {
		if _, ok := s.propose(); !ok {
			t.Errorf("%s: no nudge", name)
		}
	}
}

func TestNudgeProposalMentionsWhomTheIntentAsks(t *testing.T) {
	d, ok := stalledChanges().propose()
	if !ok {
		t.Fatal("no proposal")
	}
	if want := "@{github:alice} friendly nudge: this pull request has been waiting on you since 2026-10-14 (changes asked). Could you take a look when you have a moment?"; d.Text != want || proposedWho(d) != "@"+other {
		t.Fatalf("text %q, want %q", d.Text, want)
	}
	if !slices.Equal(d.Items, []string{"gh:fix"}) {
		t.Fatalf("items %v", d.Items)
	}
	team := stalledReview()
	team.pr.ReviewRequests = []string{"team:acme/web"}
	team.events[0].Data = "team:acme/web"
	d, ok = team.propose()
	if !ok || !strings.Contains(d.Text, "@acme/web ") || strings.Contains(d.Text, "@{") || proposedWho(d) != "@acme/web" {
		t.Fatalf("team proposal %v %q", ok, d.Text)
	}
}

// postedREST answers a comment's post at at.
type postedREST struct{ at time.Time }

func (p postedREST) REST(_ context.Context, r api.Request) (api.Response, error) {
	var in struct{ Body string }
	_ = json.Unmarshal(r.Body, &in)
	body, _ := json.Marshal(map[string]any{"id": 1001, "html_url": "https://github.com/acme/api/pull/1#issuecomment-1001", "body": in.Body, "created_at": p.at})
	return api.Response{Status: 201, Body: body}, nil
}

// post publishes d as the plugin's publisher does and answers the events
// it recorded.
func post(t *testing.T, d src.Draft) ([]pr.Event, src.Evidence) {
	t.Helper()
	var evs []pr.Event
	p := &publish.Publisher{GH: postedREST{at: now}, Viewer: func(context.Context) (string, error) { return viewer, nil }, Now: func() time.Time { return now },
		Record: func(_ context.Context, e src.Event) error {
			r, _ := pr.RefOf(e.Unit)
			evs = append(evs, pr.Event{PR: r, Kind: e.Kind, At: e.At, Data: string(e.Data)})
			return nil
		}}
	d.Delivery = "comment"
	got, err := p.Publish(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return evs, got
}

func TestIntentNudgeAndDraftNudgeShareTheWindow(t *testing.T) {
	s := stalledChanges()
	if _, ok := s.propose(); !ok {
		t.Fatal("no proposal before any nudge")
	}
	intent := s
	intent.events = append(slices.Clone(s.events), ev(1, pr.KindNudged, "", now.Add(-3*time.Hour), "@"+other))
	if d, ok := intent.propose(); ok {
		t.Fatalf("proposed %q within %s of an intent nudge", d.Text, nudge.RenudgeAfter)
	}
	later := intent
	later.at = now.Add(nudge.RenudgeAfter)
	if _, ok := later.propose(); !ok {
		t.Fatal("no proposal once the window passed")
	}
	unstarted := intent
	unstarted.events = slices.Clone(intent.events)
	unstarted.events[len(unstarted.events)-1].Key = "t9"
	unstarted.host.Book = src.Book{Turns: []src.Turn{{Seq: 9, ID: "t9", State: "dismissed"}}}
	if _, ok := unstarted.propose(); !ok {
		t.Fatal("the nudge of a turn whose agent never started holds the next")
	}

	d, _ := s.propose()
	recorded, _ := post(t, d)
	sent := s
	sent.events = append(slices.Clone(s.events), recorded...)
	if d, ok := sent.propose(); ok {
		t.Fatalf("proposed %q again after a draft nudge was sent", d.Text)
	}
	r := sent.row()
	refusal := nudge.Refusal(s.pr.Ref, []string{"@" + other}, "", r.Situation.Since, sent.events, nil, now)
	if !strings.Contains(refusal, "already nudged @"+other) {
		t.Fatalf("intent refusal %q", refusal)
	}
}

func TestDraftNudgeCommentIsANudgeComment(t *testing.T) {
	s := stalledReviewerBack()
	d, ok := s.propose()
	if !ok {
		t.Fatal("no proposal")
	}
	recorded, got := post(t, d)
	comment := ev(3, pr.KindComment, viewer, got.At.Add(time.Second), "")
	viaDraft := s
	viaDraft.events = append(slices.Clone(s.events), append(slices.Clone(recorded), comment)...)
	viaIntent := s
	viaIntent.events = append(slices.Clone(s.events), ev(3, pr.KindNudged, "", now, "@"+reviewer), comment)
	bare := s
	bare.events = append(slices.Clone(s.events), comment)

	sit := func(s pull) string {
		r := s.row().Situation
		return string(r.Kind) + "|" + r.WaitingOn + "|" + r.Since.String() + "|" + r.Reason
	}
	if got, want := sit(viaDraft), sit(viaIntent); got != want {
		t.Fatalf("after a draft nudge %s, after an intent nudge %s", got, want)
	}
	if got, want := sit(viaDraft), sit(s); got != want {
		t.Fatalf("the draft nudge's comment moved the situation: %s, before %s", got, want)
	}
	if sit(bare) == sit(s) {
		t.Fatalf("a plain comment leaves %s too; the case proves nothing", sit(bare))
	}
}

func TestNudgeSituationWithoutATime(t *testing.T) {
	t.Parallel()
	if got, err := profileTexts().Waiting([]string{"@ana"}, nudge.Situation{Kind: "draft", Reason: "draft"}); err != nil || got != "Waiting on: @ana, draft." {
		t.Fatalf("%q", got)
	}
}

func TestNudgeSituationCarriesNoCIText(t *testing.T) {
	t.Parallel()
	if got, err := profileTexts().Waiting([]string{"@ana"}, nudge.Situation{Kind: "checks", Reason: "checks failing: lint, @acme/everyone"}); err != nil || got != "Waiting on: @ana, checks failing." {
		t.Fatalf("%q", got)
	}
}

// fakeReader serves the host's books, overrides and tasks of a unit.
type fakeReader struct {
	books     []src.Book
	overrides map[string]map[string]string
	tasks     []src.Task
	asked     []string
}

func (f *fakeReader) Tasks(context.Context, []sdk.Unit) ([]src.Task, error) { return f.tasks, nil }

func (f *fakeReader) Books(context.Context, []sdk.Unit) ([]src.Book, error) { return f.books, nil }

func (f *fakeReader) RoundState(_ context.Context, u sdk.Unit) (src.RoundState, error) {
	f.asked = append(f.asked, u.Key)
	return src.RoundState{Overrides: f.overrides[u.Key]}, nil
}

func TestProposalsReadTheHostOfTheStalledPullRequestsOnly(t *testing.T) {
	fresh := pull{pr: open(4, viewer)}
	fresh.pr.ReviewRequests = []string{reviewer}
	fresh.events = []pr.Event{ev(4, pr.KindReviewRequest, viewer, now.Add(-time.Hour), reviewer)}
	held := stalledReview()
	asleep := stalledReview()
	asleep.pr.Ref = ref(3)
	rd := &fakeReader{overrides: map[string]map[string]string{held.pr.Ref.String(): {"hold": "true"}},
		tasks: []src.Task{{ID: "t-01", State: "scheduled", Category: "deferred", Units: []sdk.Unit{ref(3).Unit()}}}}
	ds, err := nudge.Proposals(context.Background(), []row.Row{stalledChanges().row(), held.row(), asleep.row(), fresh.row()}, rd, profileTexts(), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Unit != ref(1).Unit() {
		t.Fatalf("proposals %+v", ds)
	}
	if !slices.Equal(rd.asked, []string{ref(1).String(), ref(2).String(), ref(3).String()}) {
		t.Fatalf("read the round state of %v", rd.asked)
	}
}

func TestTheNudgeTextIsTheProfiles(t *testing.T) {
	t.Parallel()
	x, err := nudge.ParseTexts(`{{define "draft"}}Hello {{.Mentions}} ({{template "label" .Kind}}){{end}}{{define "waiting"}}w{{end}}{{define "label"}}<{{.}}>{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := x.Text([]string{"@alice", "@acme/web"}, nudge.Situation{Kind: "review"}); err != nil || got != "Hello @{github:alice} @acme/web (<review>)" {
		t.Fatalf("%q %v", got, err)
	}
	wantMaintainers := "Friendly nudge: this pull request has had no reviewer since 2026-10-12. Could a maintainer take a look or suggest a reviewer?"
	if got, err := profileTexts().Text([]string{nudge.Maintainers}, nudge.Situation{Kind: "no_reviewer", Since: now.Add(-10 * day)}); err != nil || got != wantMaintainers {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := nudge.ParseTexts(`{{define "draft"}}x{{end}}`); err == nil {
		t.Fatal("texts with no waiting or label template parsed")
	}
}

func TestADraftPullRequestTakesNoNudge(t *testing.T) {
	who, why := nudge.Targets(nudge.Situation{Kind: "draft", WaitsOn: "author", Reason: "draft"}, "ana", "me", nil)
	if len(who) != 0 || why != "is a draft" {
		t.Fatalf("who %v, why %q", who, why)
	}
}
