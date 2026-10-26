package brief

import (
	"context"
	"fmt"
	"testing"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// TestGitHubSourceStaleIsTheQueuesRule runs the GitHub BriefSource against
// the queue's Due and attemptOf over moved heads, edited bodies, new files
// and failures inside and past their backoff.
func TestGitHubSourceStaleIsTheQueuesRule(t *testing.T) {
	version := Version(DefaultTaxonomy)
	g := GitHubSource{Retry: time.Hour}
	edited := t0.Add(-time.Hour)
	base := dinv.PR{Ref: github.Ref{Owner: "acme", Repo: "api", Number: 1}, HeadOid: "h1", BodyEditedAt: edited, ChangedFiles: 3}
	prs := map[string]dinv.PR{"same": base}
	for name, f := range map[string]func(*dinv.PR){
		"new head":          func(p *dinv.PR) { p.HeadOid = "h2" },
		"body just edited":  func(p *dinv.PR) { p.BodyEditedAt = t0.Add(-time.Minute) },
		"body edited quiet": func(p *dinv.PR) { p.BodyEditedAt = t0.Add(-BodyQuiet) },
		"few new files":     func(p *dinv.PR) { p.ChangedFiles = 3 + RefreshFiles },
		"many new files":    func(p *dinv.PR) { p.ChangedFiles = 4 + RefreshFiles },
		"never edited":      func(p *dinv.PR) { p.BodyEditedAt = time.Time{} },
	} {
		p := base
		f(&p)
		prs[name] = p
	}
	recs := map[string]dinv.BriefRecord{
		"ok":               {Run: 1, Version: version, HeadOid: "h1", BodyEditedAt: edited, ChangedFiles: 3},
		"ok without files": {Run: 1, Version: version, HeadOid: "h1", BodyEditedAt: edited},
		"failed recently":  {Failures: 2, Error: "boom", Version: version, AttemptedAt: t0.Add(-time.Hour), AttemptOf: attemptOf(base, version)},
		"failed long ago":  {Failures: 1, Error: "boom", Version: version, AttemptedAt: t0.Add(-2 * time.Hour), AttemptOf: attemptOf(base, version)},
	}
	for pn, p := range prs {
		cur := AtOf(p)
		if got, want := g.AttemptKey(version, cur), attemptOf(p, version); got != want {
			t.Errorf("%s: attempt key %q, the queue's %q", pn, got, want)
		}
		for rn, rec := range recs {
			w := Written{At: AtOf(dinv.PR{HeadOid: rec.HeadOid, BodyEditedAt: rec.BodyEditedAt, ChangedFiles: rec.ChangedFiles}),
				Version: rec.Version, Error: rec.Error, Failures: rec.Failures, AttemptedAt: rec.AttemptedAt, AttemptOf: rec.AttemptOf}
			for _, v := range []string{version, "b1-older"} {
				if got, want := g.Stale(w, cur, v, t0), Due(p, rec, true, v, t0, time.Hour); got != want {
					t.Errorf("%s, %s at %s: stale %v, the queue's Due %v", pn, rn, v, got, want)
				}
			}
		}
	}
}

type prsByRef map[github.Ref]dinv.PR

func (m prsByRef) PR(_ context.Context, ref github.Ref) (dinv.PR, bool, error) {
	p, ok := m[ref]
	return p, ok, nil
}

func TestGitHubSourceReadsTheRevisionAndInput(t *testing.T) {
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 4}
	g := GitHubSource{Store: prsByRef{ref: {Ref: ref, HeadOid: "h9", BodyEditedAt: t0, ChangedFiles: 2}},
		Source: &fakeSource{fail: map[string]error{}}, MaxDiffBytes: 1000}
	u := unit.Unit{Provider: unit.GitHub, Key: ref.String()}
	at, err := g.Revision(ctx, u)
	if err != nil || at.Revision != "h9 2026-10-14T12:00:00.000000000Z" || string(at.Parts) != `{"changed_files":2}` {
		t.Fatalf("%+v %s %v", at, at.Parts, err)
	}
	in, err := g.Input(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if d, want := g.Describe(in), fmt.Sprintf("a pull request of 0 changed files, its diff %d bytes", in.Bytes); d != want || string(in.Parts) != `{"changed_files":0}` {
		t.Errorf("describe %q, want %q; parts %s", d, want, in.Parts)
	}
	if _, err := g.Revision(ctx, unit.Unit{Provider: "slack", Key: "C1/1"}); err == nil {
		t.Error("a Slack unit has a pull request revision")
	}
}
