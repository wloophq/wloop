package row

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
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
		"approved review head":         {SignalInLoop, SignalReady, SignalWorkspace, SignalApprovedByMe},
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

func TestApprovedByMeIsMyNewestVerdictOnSomeoneElsesOpenPullRequest(t *testing.T) {
	theirs := open(50, "ana", func(p *pr.PR) { p.Reasons = []string{"review-requested"} })
	approved := ev(50, pr.KindReview, fixtureViewer, ago(3*day), "APPROVED:h0")
	for _, c := range []struct {
		name string
		pr   pr.PR
		evs  []pr.Event
		want bool
	}{
		{"approved, then pushed", theirs, []pr.Event{approved, ev(50, pr.KindPush, "ana", ago(2*day), "")}, true},
		{"approved, then commented", theirs, []pr.Event{approved, ev(50, pr.KindReview, fixtureViewer, ago(day), "COMMENTED:h1")}, true},
		{"approved, then asked changes", theirs, []pr.Event{approved, ev(50, pr.KindReview, fixtureViewer, ago(day), "CHANGES_REQUESTED:h1")}, false},
		{"approved, then re-requested", open(50, "ana", func(p *pr.PR) { p.Reasons, p.ReviewRequests = []string{"review-requested"}, []string{fixtureViewer} }),
			[]pr.Event{approved, ev(50, pr.KindReviewRequest, "ana", ago(day), fixtureViewer)}, false},
		{"approved my own", open(50, fixtureViewer, nil), []pr.Event{approved}, false},
		{"approved, then merged", open(50, "ana", func(p *pr.PR) { p.State, p.MergedAt = "MERGED", ago(day) }), []pr.Event{approved}, false},
		{"never reviewed", theirs, nil, false},
	} {
		got := slices.Contains(on(Derive(input(fixture{pr: c.pr, events: c.evs}))), SignalApprovedByMe)
		if got != c.want {
			t.Errorf("%s: approved_by_me %v, want %v", c.name, got, c.want)
		}
	}
}
