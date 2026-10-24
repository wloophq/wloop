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
