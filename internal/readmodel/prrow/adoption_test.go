package prrow

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
)

func TestTheHostsAdoptionMakesThePullRequestMine(t *testing.T) {
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 41}
	p := dinv.PR{Ref: ref, Title: "change of bruno", Author: "bruno", State: "OPEN", HeadOid: "h1", HeadBranch: "feat/y", BaseBranch: "main",
		CreatedAt: ago(2 * day), Reasons: []string{"involved"}, ChecksState: "SUCCESS"}
	rep := reportOf(t, ghreporttest.Input{PR: p})
	if rep.Payload.Override.Adopted || slices.Contains(rep.Views, api.ViewMine) {
		t.Fatalf("the plugin reported an adoption it does not hold: %+v, views %v", rep.Payload.Override, rep.Views)
	}
	d := fixtureDeriver(t, fixtureNow, nil)
	r, payload := d.Parts(rep, nil, dinv.Override{PR: ref, Adopted: true})
	if !payload.Override.Adopted || !slices.Contains(r.Views, api.ViewMine) {
		t.Fatalf("the host's adoption is not the row's: %+v, views %v", payload.Override, r.Views)
	}
}
