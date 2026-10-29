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

func TestTheSignalsAreWhatTheRowsViewsSay(t *testing.T) {
	fs := append(fixtures(),
		fixture{name: "stalled outside the scope", pr: open(40, "ana", func(p *pr.PR) {
			p.Reasons, p.ReviewRequests, p.CreatedAt = nil, []string{fixtureViewer}, ago(60*day)
		})},
		fixture{name: "closed in a review workspace", pr: open(41, "ana", func(p *pr.PR) {
			p.State, p.ClosedAt, p.Workspace = "CLOSED", ago(day), pr.Workspace{ID: "ws-r", IsReview: true}
		})})
	seen := map[string]bool{}
	for _, f := range fs {
		r := Derive(input(f))
		got := on(r)
		for _, c := range []struct {
			signal string
			want   bool
		}{
			{SignalStalled, slices.Contains(r.Views, ViewStalled)},
			{SignalInLoop, slices.Contains(r.Views, ViewLoop) && r.PR.State == "OPEN"},
			{SignalReady, slices.Contains(r.Views, ViewApproved)},
		} {
			if slices.Contains(got, c.signal) != c.want {
				t.Errorf("%s: %s is %v, want %v (views %v)", f.name, c.signal, !c.want, c.want, r.Views)
			}
			seen[c.signal] = seen[c.signal] || c.want
		}
		if f.name == "stalled outside the scope" && (!r.Stalled() || slices.Contains(got, SignalStalled)) {
			t.Errorf("a stalled pull request with no reason to follow it: stalled %v, signals %v", r.Stalled(), got)
		}
	}
	for _, s := range []string{SignalStalled, SignalInLoop, SignalReady} {
		if !seen[s] {
			t.Errorf("no fixture has %s on", s)
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
