package catalog

import (
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func TestFactFacetsWriteEachFactAsText(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	defs := []intentload.FacetDef{{ID: "state", Fact: "task.state"}, {ID: "none", Fact: "task.placement"}, {ID: "focus", Fact: "task.review_due"},
		{ID: "off", Fact: "task.children_mine"}, {ID: "gates", Fact: "task.gates_open"}, {ID: "closed", Fact: "task.gates_closed"},
		{ID: "moved", Fact: "task.moved_at"}, {ID: "never", Fact: "task.woke_at"}, {ID: "signal", Fact: "task.link_signals"},
		{ID: "type", Fact: "brief.kind"}, {ID: "missing", Fact: "task.nothing"}}
	f := workflow.Facts{"task.state": "next", "task.placement": "", "task.review_due": true, "task.children_mine": false, "task.gates_open": 2,
		"task.gates_closed": 0, "task.moved_at": at, "task.woke_at": time.Time{}, "task.link_signals": []string{"stalled", "ready"}}
	want := map[string][]string{"state": {"next"}, "focus": {"yes"}, "gates": {"2"}, "moved": {"2026-10-08T10:00:00Z"}, "signal": {"stalled", "ready"}}
	if got := FactFacets(defs, f); !reflect.DeepEqual(got, want) {
		t.Fatalf("facets %v, want %v", got, want)
	}
}

func TestRecordFacetsReadTheFieldsOfTheirKind(t *testing.T) {
	t.Parallel()
	defs := []intentload.FacetDef{{ID: "type", Fact: "brief.kind"}, {ID: "tag", Fact: "brief.tags"}, {ID: "risk", Fact: "brief.risk"},
		{ID: "n", Fact: "brief.files"}, {ID: "ok", Fact: "brief.safe"}, {ID: "state", Fact: "task.state"}, {ID: "other", Fact: "plan.kind"}}
	data := []byte(`{"kind":"feature","tags":["db","api"],"risk":"","files":3,"safe":true,"goal":"Export"}`)
	want := map[string][]string{"type": {"feature"}, "tag": {"db", "api"}, "n": {"3"}, "ok": {"yes"}}
	if got := RecordFacets(defs, "brief", data); !reflect.DeepEqual(got, want) {
		t.Fatalf("facets %v, want %v", got, want)
	}
	if got := RecordFacets(defs, "brief", []byte(`not json`)); len(got) != 0 {
		t.Fatalf("facets of bad data %v", got)
	}
}

func TestMergeFacetsJoinsTheLayersAsEachFacetSays(t *testing.T) {
	t.Parallel()
	defs := []intentload.FacetDef{{ID: "waits-on", Merge: "rows-first", Units: "all"}, {ID: "type", Merge: "rows-first", Units: "all"},
		{ID: "source", Merge: "union", Units: "all"}, {ID: "tag", Merge: "union", Units: "all"}, {ID: "checks", Merge: "union", Units: "primary"}}
	own := map[string][]string{"waits-on": {"github:ana"}, "type": {"feature"}, "source": {"slack", "github"}, "tag": {"db"}}
	rows := []map[string][]string{
		{"where": {"general"}, "tag": {"api", "db"}, "checks": {"none"}},
		nil,
		{"where": {"acme/api", "general"}, "waits-on": {"me"}, "checks": {"failure"}, "ci": {"red"}},
	}
	want := map[string][]string{"waits-on": {"me"}, "type": {"feature"}, "source": {"slack", "github"}, "tag": {"db", "api"},
		"checks": {"none"}, "where": {"general", "acme/api"}, "ci": {"red"}}
	if got := MergeFacets(defs, own, rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("facets %v, want %v", got, want)
	}
	if got := MergeFacets(defs, nil, nil); len(got) != 0 {
		t.Fatalf("facets of nothing %v", got)
	}
}

func TestATurnFacetFollowsTheMergedValuesItTakesTheTurnOf(t *testing.T) {
	t.Parallel()
	defs := []intentload.FacetDef{{ID: "waits-on", Merge: "rows-first", Units: "all"}, {ID: "turn", Merge: "union", Units: "all", TurnOf: "waits-on"}}
	for _, c := range []struct {
		name string
		own  map[string][]string
		rows []map[string][]string
		want string
	}{
		{"a row's person wins over the task's own owner", map[string][]string{"waits-on": {"me"}, "turn": {"me"}}, []map[string][]string{{"waits-on": {"ana"}, "turn": {"others"}}}, "others"},
		{"a row waiting on the viewer", map[string][]string{"waits-on": {"ana"}, "turn": {"others"}}, []map[string][]string{{"waits-on": {"ana", "me"}}}, "me"},
		{"no row, the task's own owner", map[string][]string{"waits-on": {"me"}, "turn": {"me"}}, nil, "me"},
		{"nobody", map[string][]string{"turn": {"nobody"}}, []map[string][]string{{"turn": {"others"}}}, "nobody"},
	} {
		got := MergeFacets(defs, c.own, c.rows)
		if !reflect.DeepEqual(got["turn"], []string{c.want}) {
			t.Errorf("%s: turn %v, want %s", c.name, got["turn"], c.want)
		}
	}
}
