package slack

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/store"
)

// rowFacets is testdata/row-facets.json: the facets a Slack row holds by its situation, and the ids of the facets
// that hold its since and its channel. Clients that may not import this plugin build their Slack rows from it.
type rowFacets struct {
	BySituation map[string]map[string][]string `json:"by_situation"`
	Since       string                         `json:"since"`
	Channel     string                         `json:"channel"`
}

// TestTheSharedRowFacetsAreTheOnesSlackDerives derives a row for each situation testdata/row-facets.json lists
// and checks it holds those facets, and its since and channel under the ids the file names.
func TestTheSharedRowFacetsAreTheOnesSlackDerives(t *testing.T) {
	raw, err := os.ReadFile("testdata/row-facets.json")
	if err != nil {
		t.Fatal(err)
	}
	var want rowFacets
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if want.Since != slackrows.FacetAge || want.Channel != slackrows.FacetWhere {
		t.Errorf("the file names since %q and channel %q, Slack %q and %q", want.Since, want.Channel, slackrows.FacetAge, slackrows.FacetWhere)
	}
	for _, kind := range slices.Sorted(maps.Keys(want.BySituation)) {
		f := newFixture(t)
		f.login(t)
		f.people(t)
		u := f.thread(t)
		f.state(t, u, store.ExtractionState{Verdict: kind, At: f.now})
		waiter, owner := otherPerson, mePerson
		if kind == string(slackrows.KindWaitsOnViewer) {
			waiter, owner = mePerson, otherPerson
		}
		applyOps(t, f.ledger(), items.Extracted, createOp(u, "e1", 1, owner, waiter))
		r, ok := f.row(t, u)
		if !ok {
			t.Fatalf("%s: no row", kind)
		}
		for id, vs := range want.BySituation[kind] {
			if !slices.Equal(r.Facets[id], vs) {
				t.Errorf("%s: facet %s is %q, the file says %q", kind, id, r.Facets[id], vs)
			}
		}
		if got := r.Facets[want.Since]; len(got) != 1 || got[0] != r.Summary.Since.UTC().Format(time.RFC3339Nano) {
			t.Errorf("%s: since facet %q, summary since %v", kind, got, r.Summary.Since)
		}
		if len(r.Facets[want.Channel]) != 1 {
			t.Errorf("%s: channel facet %q", kind, r.Facets[want.Channel])
		}
	}
}
