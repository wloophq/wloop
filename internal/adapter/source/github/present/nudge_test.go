package present

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestNudgeSituationWithoutATime(t *testing.T) {
	t.Parallel()
	if got := nudgeSituation([]string{"@ana"}, api.Row{Kind: "draft", Summary: api.Summary{Reason: "draft"}}); got != "Waiting on: @ana, draft." {
		t.Fatalf("%q", got)
	}
}

func TestNudgeSituationCarriesNoCIText(t *testing.T) {
	t.Parallel()
	r := api.Row{Kind: "checks", Summary: api.Summary{Reason: "checks failing: lint, @acme/everyone"}}
	if got := nudgeSituation([]string{"@ana"}, r); got != "Waiting on: @ana, checks failing." {
		t.Fatalf("%q", got)
	}
}
