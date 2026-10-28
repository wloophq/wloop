package row

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
	"github.com/wloophq/wloop/plugins/github/store"
)

var update = flag.Bool("update", false, "rewrite testdata/reports.golden.json")

// fixtureNow is the instant every fixture is reported at.
var fixtureNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

const (
	fixtureViewer = "me-login"
	day           = 24 * time.Hour
)

func ago(d time.Duration) time.Time { return fixtureNow.Add(-d) }

// fixture is one pull request with everything its report derives from.
type fixture struct {
	name     string
	pr       pr.PR
	events   []pr.Event
	override pr.Override
}

func ref(n int) pr.Ref { return pr.Ref{Owner: "acme", Repo: "api", Number: n} }

func open(n int, author string, edit func(*pr.PR)) pr.PR {
	p := pr.PR{Ref: ref(n), Title: "change " + author, Author: author, State: "OPEN", HeadOid: "h1", HeadBranch: "feat/x", BaseBranch: "main",
		CreatedAt: ago(time.Duration(n) * 13 * time.Hour), Reasons: []string{"involved"}, ChecksState: "SUCCESS", ChangedFiles: n}
	if edit != nil {
		edit(&p)
	}
	return p
}

func ev(n int, kind, actor string, at time.Time, data string) pr.Event {
	return pr.Event{PR: ref(n), Kind: kind, Key: kind + at.Format(time.RFC3339), Actor: actor, At: at, Data: data}
}

// fixtures cover every situation the plugin derives, both roles,
// workspaces and the overrides the plugin keeps.
func fixtures() []fixture {
	reviewWS := pr.Workspace{ID: "ws-r", DisplayName: "review", Status: "in-review", IsReview: true}
	authorWS := pr.Workspace{ID: "ws-a", DisplayName: "author", Status: "in-progress"}
	return []fixture{
		{name: "review asked of me", pr: open(1, "ana", func(p *pr.PR) { p.ReviewRequests = []string{fixtureViewer} }),
			events: []pr.Event{ev(1, pr.KindReviewRequest, "ana", ago(3*day), fixtureViewer)}},
		{name: "review asked of a team", pr: open(2, "bob", func(p *pr.PR) { p.ReviewRequests = []string{"team:acme/core", "carol"} })},
		{name: "review asked of another", pr: open(3, "bob", func(p *pr.PR) { p.ReviewRequests = []string{"carol"} })},
		{name: "mine without reviewer", pr: open(4, fixtureViewer, nil)},
		{name: "mine draft", pr: open(5, fixtureViewer, func(p *pr.PR) { p.IsDraft = true; p.Workspace = authorWS })},
		{name: "mine checks failing", pr: open(6, fixtureViewer, func(p *pr.PR) {
			p.ChecksState = "FAILURE"
			p.FailingChecks = []pr.FailingCheck{{Name: "lint"}, {Name: "unit"}}
		})},
		{name: "mine changes asked", pr: open(7, fixtureViewer, nil),
			events: []pr.Event{ev(7, pr.KindReview, "carol", ago(9*day), "CHANGES_REQUESTED:h1")}},
		{name: "mine can merge", pr: open(8, fixtureViewer, func(p *pr.PR) { p.ReviewDecision = "APPROVED"; p.Workspace = authorWS }),
			events: []pr.Event{ev(8, pr.KindReview, "carol", ago(2*time.Hour), "APPROVED:h1")}},
		{name: "mine conflicts", pr: open(9, fixtureViewer, func(p *pr.PR) {
			p.ReviewDecision, p.Mergeable, p.MergeableAt = "APPROVED", "CONFLICTING", ago(time.Hour)
		})},
		{name: "reply due on theirs", pr: open(10, "ana", nil), events: []pr.Event{ev(10, pr.KindComment, fixtureViewer, ago(5*day), "")}},
		{name: "reviewer back", pr: open(11, "ana", nil),
			events: []pr.Event{ev(11, pr.KindComment, fixtureViewer, ago(6*day), ""), ev(11, pr.KindComment, "ana", ago(5*day), "")}},
		{name: "merged", pr: open(13, "ana", func(p *pr.PR) { p.State = "MERGED"; p.MergedAt = ago(day); p.Workspace = reviewWS }),
			events: []pr.Event{ev(13, pr.KindMerged, "ana", ago(day), "")}},
		{name: "closed without workspace", pr: open(14, "ana", func(p *pr.PR) { p.State = "CLOSED"; p.ClosedAt = ago(day) })},
		{name: "held by the cap", pr: open(18, "ana", func(p *pr.PR) { p.Workspace = reviewWS }),
			events: []pr.Event{ev(18, pr.KindRoundHeld, "", ago(time.Hour), "cap reached")}},
		{name: "round blocked", pr: open(19, "ana", nil), events: []pr.Event{ev(19, pr.KindRoundBlocked, fixtureViewer, ago(2*time.Hour), "no access")}},
		{name: "to address", pr: open(20, fixtureViewer, nil), events: []pr.Event{ev(20, pr.KindWouldAddress, "", ago(3*time.Hour), "carol asked")}},
		{name: "workspace agent waiting", pr: open(25, "ana", func(p *pr.PR) {
			p.Workspace = reviewWS
			p.Workspace.AgentStates = []string{"waiting"}
		})},
		{name: "adopted outside scope", pr: open(26, "ana", func(p *pr.PR) { p.Reasons = nil; p.ReviewRequests = []string{"carol"} }),
			override: pr.Override{Adopted: true}},
		{name: "tracked", pr: open(27, "bob", func(p *pr.PR) { p.Reasons = []string{"tracked"} }), override: pr.Override{Tracked: true}},
		{name: "approved review head", pr: open(28, "ana", func(p *pr.PR) { p.Workspace = reviewWS; p.ReviewRequests = []string{"carol"} }),
			events: []pr.Event{ev(28, pr.KindReview, fixtureViewer, ago(day), "APPROVED:h1")}},
		{name: "old stalled review", pr: open(31, "ana", func(p *pr.PR) {
			p.ReviewRequests = []string{fixtureViewer}
			p.CreatedAt = ago(60 * day)
		}), events: []pr.Event{ev(31, pr.KindOpened, "ana", ago(60*day), "")}},
	}
}

func input(f fixture) Input {
	f.override.Ref = f.pr.Ref
	return Input{PR: f.pr, Events: f.events, Override: f.override, Viewer: fixtureViewer, Teams: []string{"acme/core"},
		Stalled: situation.Thresholds{Review: 2 * day, Reply: day, Merge: day}, MaxDetailAge: time.Hour, Loop: "the loop", Now: fixtureNow}
}

// TestTheReportsGoldenIsCurrent holds what the plugin reports of every
// fixture to testdata/reports.golden.json: the situation, the payload, the
// views, the facets, the card, the items, the signals and the facts.
func TestTheReportsGoldenIsCurrent(t *testing.T) {
	type named struct {
		Name    string       `json:"name"`
		Payload Payload      `json:"payload"`
		Report  src.Snapshot `json:"report"`
	}
	var got []named
	for _, f := range fixtures() {
		r := Derive(input(f))
		got = append(got, named{Name: f.name, Payload: r.Payload, Report: r.Report()})
	}
	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("testdata", "reports.golden.json")
	if *update {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run go test -run TestTheReportsGoldenIsCurrent -update", err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("the reports differ from %s; rerun with -update and review the diff", path)
	}
}

func TestLastHumanSkipsViewerAndBots(t *testing.T) {
	f := fixtures()[0]
	in := input(f)
	in.Ignore = []string{"copilot"}
	e := func(kind, actor string, at time.Time) pr.Event {
		return pr.Event{PR: f.pr.Ref, Kind: kind, Key: actor + at.String(), Actor: actor, At: at}
	}
	in.Events = []pr.Event{e(pr.KindComment, fixtureViewer, ago(time.Hour)), e(pr.KindReview, "renovate[bot]", ago(time.Minute)),
		e(pr.KindComment, "Copilot", ago(time.Second)), e("muted", "ana", ago(time.Second))}
	if h := Derive(in).Payload.LastHuman; h != nil {
		t.Errorf("viewer and bots only: %+v", h)
	}
	in.Events = append(in.Events, e(pr.KindComment, "bia", ago(2*time.Hour)), e(pr.KindReview, "carol", ago(30*time.Minute)))
	r := Derive(in)
	if h := r.Payload.LastHuman; h == nil || h.Who != "carol" || h.What != pr.KindReview || !h.At.Equal(ago(30*time.Minute)) || h.Link != r.Link() {
		t.Errorf("last human %+v", h)
	}
}

func TestCardFromRow(t *testing.T) {
	in := input(fixture{pr: open(1, fixtureViewer, func(p *pr.PR) {
		p.ChecksState, p.FailingChecks = "FAILURE", []pr.FailingCheck{{Name: "lint"}}
	})})
	r := Derive(in)
	c := r.Card()
	if c.Label != "acme/api#1" || c.Title != "change "+fixtureViewer || c.State != "open" || c.Outcome != "" {
		t.Errorf("card %+v", c)
	}
	if !slices.Contains(c.Signals, src.CardSignal{Key: "checks", Value: "failing: lint", Tone: ToneBad}) ||
		c.Signals[0] != (src.CardSignal{Key: SignalSituation, Value: string(situation.KindChecks), Tone: ToneNeutral}) {
		t.Errorf("signals %+v", c.Signals)
	}
	if c.Why != r.Situation.Reason || c.Why == "" || !c.Since.Equal(r.Situation.Since) || c.Link != r.Link() {
		t.Errorf("why %q since %v link %q", c.Why, c.Since, c.Link)
	}
	if want := []src.Person{{Key: "github:" + fixtureViewer, Name: fixtureViewer}}; !slices.EqualFunc(c.WaitsOn, want, samePerson) {
		t.Errorf("waits on %+v", c.WaitsOn)
	}
	if c.TurnsLink != "#unit=github/acme/api#1" || !c.StartedByMe || !c.Cares || c.ClosurePosted {
		t.Errorf("card %+v", c)
	}
}

func samePerson(a, b src.Person) bool { return a.Key == b.Key && a.Name == b.Name }

func TestCardLastHuman(t *testing.T) {
	at := ago(time.Hour)
	in := input(fixture{pr: open(1, fixtureViewer, nil), events: []pr.Event{ev(1, pr.KindComment, "bruno", at, "")}})
	h := Derive(in).Card().LastHuman
	if h == nil || h.Who.Key != "github:bruno" || h.Who.Name != "bruno" || h.What != pr.KindComment || !h.At.Equal(at) || h.Link == "" {
		t.Fatalf("last human %+v", h)
	}
	in.Events = []pr.Event{ev(1, pr.KindComment, fixtureViewer, at, "")}
	if c := Derive(in).Card(); c.LastHuman != nil {
		t.Errorf("last human %+v", c.LastHuman)
	}
}

func TestCardDone(t *testing.T) {
	for _, tc := range []struct{ state, want, outcome string }{{"MERGED", "merged", "done"}, {"CLOSED", "closed", "moot"}} {
		r := Derive(input(fixture{pr: open(1, "ana", func(p *pr.PR) { p.State, p.ChecksState = tc.state, "FAILURE" })}))
		c := r.Card()
		if c.State != tc.want || c.Outcome != tc.outcome || !c.ClosurePosted {
			t.Errorf("%s: %+v", tc.state, c)
		}
		if slices.ContainsFunc(c.Signals, func(s src.CardSignal) bool { return s.Tone == ToneBad }) {
			t.Errorf("%s: signals %+v", tc.state, c.Signals)
		}
		if !r.Due().IsZero() {
			t.Errorf("%s: due %v", tc.state, r.Due())
		}
		if got := r.Items(); len(got) != 0 {
			t.Errorf("%s: items %+v", tc.state, got)
		}
	}
}

func TestAStallIsTheHostsToResolveInWorkingDays(t *testing.T) {
	friday := time.Date(2026, 10, 2, 17, 0, 0, 0, time.UTC)
	in := input(fixture{pr: open(1, "ana", func(p *pr.PR) { p.ReviewRequests, p.CreatedAt = []string{"carol"}, friday.Add(-time.Hour) }),
		events: []pr.Event{ev(1, pr.KindReviewRequest, "ana", friday, "carol")}})
	in.Now = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	r := Derive(in)
	if v := r.Situation.Stalled; !reflect.DeepEqual(v, src.After(friday, 2*day, src.WorkingDays)) {
		t.Fatalf("stalled %+v", v)
	}
	if r.Stalled() || slices.Contains(r.Views, ViewStalled) {
		t.Fatalf("stalled across the weekend: views %v", r.Views)
	}
	if due := r.Due(); !due.Equal(time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("due %v, want the next day", due)
	}
	in.Now = time.Date(2026, 10, 6, 23, 0, 0, 0, time.UTC)
	if r := Derive(in); r.Due().After(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("due %v, past the stall", r.Due())
	}
}

func TestStaleIsADetailOlderThanFresh(t *testing.T) {
	in := input(fixtures()[0])
	fact := func(r Row, name string) src.Value {
		for _, f := range r.Facts() {
			if f.Name == name {
				return f.Value
			}
		}
		t.Fatalf("no %s", name)
		return src.Value{}
	}
	if v := fact(Derive(in), "pr.stale"); !reflect.DeepEqual(v, src.Bool(true)) {
		t.Fatalf("no detail: %+v", v)
	}
	in.Detail = &store.Detail{ReadAt: ago(time.Minute)}
	if v := fact(Derive(in), "pr.stale"); v.Kind != src.KindAfter || !v.Time.Equal(ago(time.Minute)) {
		t.Fatalf("a detail read: %+v", v)
	}
}
