package row

import (
	"reflect"
	"slices"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
	"github.com/wloophq/wloop/plugins/github/store"
)

// on are the names of the row's signals that are on.
func on(r Row) []string {
	var out []string
	for _, s := range r.Signals() {
		if s.On {
			out = append(out, s.Name)
		}
	}
	return out
}

func TestTheSignalsOfEachFixture(t *testing.T) {
	fs := append(fixtures(),
		fixture{name: "stalled outside the scope", pr: open(40, "ana", func(p *pr.PR) {
			p.Reasons, p.ReviewRequests, p.CreatedAt = nil, []string{fixtureViewer}, ago(60*day)
		})},
		fixture{name: "closed in a review workspace", pr: open(41, "ana", func(p *pr.PR) {
			p.State, p.ClosedAt, p.Workspace = "CLOSED", ago(day), pr.Workspace{ID: "ws-r", IsReview: true}
		})})
	want := map[string][]string{
		"mine without reviewer":        {SignalStalled},
		"mine draft":                   {SignalWorkspace},
		"mine checks failing":          {SignalStalled},
		"mine changes asked":           {SignalStalled},
		"mine can merge":               {SignalReady, SignalWorkspace},
		"reply due on theirs":          {SignalStalled},
		"reviewer back":                {SignalStalled},
		"merged":                       {SignalWorkspace},
		"held by the cap":              {SignalInLoop, SignalWorkspace},
		"workspace agent waiting":      {SignalInLoop, SignalWorkspace},
		"tracked":                      {SignalStalled},
		"approved review head":         {SignalInLoop, SignalReady, SignalWorkspace},
		"old stalled review":           {SignalStalled},
		"closed in a review workspace": {SignalWorkspace},
	}
	for _, f := range fs {
		if got := on(Derive(input(f))); !slices.Equal(got, want[f.name]) {
			t.Errorf("%s: signals on %q, want %q", f.name, got, want[f.name])
		}
	}
}

func TestAStalledSignalSaysSinceWhen(t *testing.T) {
	r := Derive(input(fixtures()[len(fixtures())-1]))
	for _, s := range r.Signals() {
		if s.Name == SignalStalled && (!s.On || s.Since.IsZero() || s.Since.After(fixtureNow) || fixtureNow.Sub(s.Since) > 60*24*time.Hour) {
			t.Fatalf("stalled %+v", s)
		}
	}
}

func TestSomeoneElsesPullRequestMyNewestVerdictApprovesAsksNothing(t *testing.T) {
	theirs := open(50, "ana", func(p *pr.PR) { p.Reasons = []string{"review-requested"} })
	requested := open(50, "ana", func(p *pr.PR) { p.Reasons, p.ReviewRequests = []string{"review-requested"}, []string{fixtureViewer} })
	approved := ev(50, pr.KindReview, fixtureViewer, ago(3*day), "APPROVED:h0")
	for _, c := range []struct {
		name     string
		pr       pr.PR
		override pr.Override
		evs      []pr.Event
		asks     bool
	}{
		{"approved, then pushed", theirs, pr.Override{}, []pr.Event{approved, ev(50, pr.KindPush, "ana", ago(2*day), "")}, false},
		{"approved, then commented", theirs, pr.Override{}, []pr.Event{approved, ev(50, pr.KindReview, fixtureViewer, ago(day), "COMMENTED:h1")}, false},
		{"approved, then asked changes", theirs, pr.Override{}, []pr.Event{approved, ev(50, pr.KindReview, fixtureViewer, ago(day), "CHANGES_REQUESTED:h1")}, true},
		{"approved, then re-requested", requested, pr.Override{}, []pr.Event{approved, ev(50, pr.KindReviewRequest, "ana", ago(day), fixtureViewer)}, true},
		{"approved, re-requested, then the request withdrawn", theirs, pr.Override{}, []pr.Event{approved, ev(50, pr.KindReviewRequest, "ana", ago(day), fixtureViewer)}, false},
		{"approved, then listed as requested with no event, which the situation reads as asking you", requested, pr.Override{}, []pr.Event{approved}, true},
		{"approved and tracked", theirs, pr.Override{Tracked: true}, []pr.Event{approved}, true},
		{"approved, then adopted", theirs, pr.Override{Adopted: true}, []pr.Event{approved}, true},
		{"approved, the approval dismissed", theirs, pr.Override{}, []pr.Event{approved, ev(50, pr.KindReview, fixtureViewer, ago(3*day), "DISMISSED:h0")}, true},
		{"approved my own", open(50, fixtureViewer, func(p *pr.PR) { p.Reasons = []string{"author"} }), pr.Override{}, []pr.Event{approved}, true},
		{"never reviewed", requested, pr.Override{}, nil, true},
	} {
		got := len(Derive(input(fixture{pr: c.pr, override: c.override, events: c.evs})).Items()) > 0
		if got != c.asks {
			t.Errorf("%s: asks %v, want %v", c.name, got, c.asks)
		}
	}
}

func TestResolvedThreadsSettleAReviewersCommentOnlyWhenThePullRequestHasThreads(t *testing.T) {
	p := open(51, "ana", func(p *pr.PR) { p.ReviewRequests, p.ReviewDecision = []string{"dave"}, "REVIEW_REQUIRED" })
	in := input(fixture{pr: p, events: []pr.Event{ev(51, pr.KindPush, "ana", ago(5*day), ""),
		ev(51, pr.KindReview, "carla", ago(4*day), "COMMENTED:h1"), ev(51, pr.KindReview, "bia", ago(3*day), "APPROVED:h1")}})
	for name, c := range map[string]struct {
		detail *store.Detail
		want   situation.Kind
	}{
		"no detail":                         {nil, situation.KindReply},
		"no thread, a top comment":          {&store.Detail{ThreadsRead: true}, situation.KindReply},
		"a thread open":                     {&store.Detail{ThreadsRead: true, ThreadsTotal: 2, OpenThreads: []pr.Thread{{}}}, situation.KindReply},
		"threads not read":                  {&store.Detail{ThreadsTotal: 2}, situation.KindReply},
		"resolved, read before the comment": {&store.Detail{ThreadsRead: true, ThreadsTotal: 2, ReadAt: ago(5 * day)}, situation.KindReply},
		"more threads than a read holds":    {&store.Detail{ThreadsRead: true, ThreadsTotal: api.MaxThreads + 1, ReadAt: ago(2 * day)}, situation.KindReply},
		"every thread resolved":             {&store.Detail{ThreadsRead: true, ThreadsTotal: 2, ReadAt: ago(2 * day)}, situation.KindReview},
	} {
		in.Detail = c.detail
		if got := Derive(in).Kind(); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestAStalledCardSaysSoOnItsSituationLine(t *testing.T) {
	f := fixtures()[slices.IndexFunc(fixtures(), func(f fixture) bool { return f.name == "mine changes asked" })]
	r := Derive(input(f))
	if r.Kind() != situation.KindChanges || !r.Stalled() {
		t.Fatalf("fixture is %s, stalled %v", r.Kind(), r.Stalled())
	}
	sigs := r.Card().Signals
	if slices.ContainsFunc(sigs, func(s src.CardSignal) bool { return s.Key == "stalled" }) {
		t.Errorf("a separate stalled signal: %+v", sigs)
	}
	want := src.CardSignal{Key: SignalSituation, Value: string(situation.KindChanges), Tone: ToneNeutral,
		Notes: []src.SignalNote{{Code: NoteStalled, Tone: ToneWarn, Params: map[string]string{"days": "7"}}}}
	if !reflect.DeepEqual(sigs[0], want) {
		t.Errorf("situation signal %+v, want %+v", sigs[0], want)
	}
	want2 := src.CardSignal{Key: SignalReview, Value: ReviewChangesRequested, Tone: ToneWarn}
	if !slices.ContainsFunc(sigs, func(s src.CardSignal) bool { return reflect.DeepEqual(s, want2) }) {
		t.Errorf("signals %+v, want %+v among them", sigs, want2)
	}
}

func TestEachCardSignalIsACode(t *testing.T) {
	want := map[string][]string{
		"mine checks failing": {"situation=checks", "checks=failing_named names=lint, unit"},
		"mine can merge":      {"situation=merge", "checks=passing", "review=approved"},
		"mine conflicts":      {"situation=conflicts", "checks=passing", "merge=conflicts"},
		"merged":              {"situation=done"},
	}
	for _, f := range fixtures() {
		w, ok := want[f.name]
		if !ok {
			continue
		}
		var got []string
		for _, s := range Derive(input(f)).Card().Signals {
			line := s.Key + "=" + s.Value
			if n := s.Params["names"]; n != "" {
				line += " names=" + n
			}
			got = append(got, line)
		}
		if !slices.Equal(got, w) {
			t.Errorf("%s: signals %q, want %q", f.name, got, w)
		}
	}
}

func TestStalledDaysAreWholeWorkingDaysAtLeastOne(t *testing.T) {
	monday := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		now  time.Time
		want string
	}{
		{monday.Add(6 * time.Hour), "1"},
		{monday.Add(4 * 24 * time.Hour), "4"},
		{monday.Add(7 * 24 * time.Hour), "5"},
	} {
		if got := stalledDays(monday, c.now); got != c.want {
			t.Errorf("at %v: %s working days, want %s", c.now, got, c.want)
		}
	}
}
