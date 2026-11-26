package catalog

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestUnitStatesOfferOnlyTheIntentsServingTheProvider(t *testing.T) {
	l := bundled(t)
	slack := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	got := UnitStates(Facts{Intents: l, Row: api.Row{Unit: slack}, Open: true, Tracked: true})
	var ids []string
	for _, st := range got {
		ids = append(ids, st.ID)
	}
	if !slices.Contains(ids, string(dintent.Act)) {
		t.Fatalf("states %v, want act", ids)
	}
	for _, id := range []string{"review", "address", "open", "hold", "answer", "run_now"} {
		if slices.Contains(ids, id) {
			t.Errorf("states %v offer %s on a Slack row", ids, id)
		}
	}
	if st := got[slices.Index(ids, string(dintent.Act))]; !st.Enabled {
		t.Errorf("act %+v, want enabled: a Slack unit is the user's", st)
	}
	if got := UnitStates(Facts{Row: api.Row{Unit: slack}}); got == nil || len(got) != 0 {
		t.Errorf("without intents: %v, want an empty list", got)
	}
}

func TestRankPutsTheUnitsProviderAndTheUserFirst(t *testing.T) {
	spec := func(src, plugin string) api.ActionSpec { return api.ActionSpec{Source: src, Plugin: plugin} }
	user := filepath.Join(string(filepath.Separator), "home", "u", "intents", "x", "intent.toml")
	bundledSrc := "intents/x/intent.toml"
	order := []api.ActionSpec{
		spec(SourceBuiltin, "slack"), spec(SourceBuiltin, ""),
		spec(user, "slack"), spec(bundledSrc, "slack"),
		spec(user, ""), spec(bundledSrc, ""),
	}
	for i := 1; i < len(order); i++ {
		if a, b := Rank(order[i-1], "slack"), Rank(order[i], "slack"); a >= b {
			t.Errorf("rank of %+v is %d, not below %+v's %d", order[i-1], a, order[i], b)
		}
	}
}

func TestUnitStatesOfferCloseOnAnOpenUnitThatClosesWithNoMessage(t *testing.T) {
	l := bundled(t)
	slack := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	closeOf := func(f Facts) (api.ActionState, bool) {
		f.Intents = l
		i := slices.IndexFunc(UnitStates(f), func(st api.ActionState) bool { return st.ID == UnitClose })
		if i < 0 {
			return api.ActionState{}, false
		}
		return UnitStates(f)[i], true
	}
	inView := api.Row{Unit: slack, Views: []string{"slack"}}
	for name, f := range map[string]Facts{
		"open row":     {Row: inView, Open: true, Closes: true},
		"resolved row": {Row: inView, Closes: true},
	} {
		if st, ok := closeOf(f); !ok || !st.Enabled {
			t.Errorf("close on a %s in a view: %+v %v, want enabled", name, st, ok)
		}
	}
	for name, f := range map[string]Facts{
		"no silent closure": {Row: inView, Open: true},
		"row in no view":    {Row: api.Row{Unit: slack, Views: []string{}}, Open: true, Closes: true},
		"row with an open item": {Row: api.Row{Unit: slack, Views: []string{"slack"}, Items: []api.RowItem{{ID: "i1", State: "open"}}},
			Open: true, Closes: true},
		"pull request": {Row: api.Row{Unit: pr, Views: []string{"slack"}}, PR: pr, Open: true, Closes: true},
	} {
		if st, ok := closeOf(f); ok {
			t.Errorf("%s: close offered %+v", name, st)
		}
	}
	i := slices.IndexFunc(Specs(l), func(s api.ActionSpec) bool { return s.ID == UnitClose })
	if i < 0 || Specs(l)[i].Label == "" || !slices.Contains(Specs(l)[i].Surfaces, SurfaceRow) || !slices.Contains(Specs(l)[i].Surfaces, SurfaceTask) {
		t.Fatalf("close spec %v, want one worded and offered on rows and on a task's page", i)
	}
	if got := States(Facts{Intents: l, PR: pr, Row: api.Row{Unit: pr}, Open: true, Tracked: true, Closes: true}); slices.ContainsFunc(got, func(st api.ActionState) bool { return st.ID == UnitClose }) {
		t.Errorf("a pull request's row offers close: %v", got)
	}
}
