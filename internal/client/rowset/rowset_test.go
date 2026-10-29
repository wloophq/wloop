package rowset

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var at = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func row(n int, title string) api.Row {
	label := "acme/api#" + strconv.Itoa(n)
	return api.Row{Unit: unit.Unit{Provider: unit.GitHub, Key: label}, Label: label, Title: title, Kind: "review",
		Views: []string{"mine"}, Summary: api.Summary{Since: at.Add(-time.Duration(n) * time.Hour)}}
}

func titles(rows []api.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}

func full(epoch string, rev uint64, rows ...api.Row) api.RowsPage {
	return api.RowsPage{Epoch: epoch, Rev: rev, Full: true, Rows: rows, Removed: []unit.Unit{}}
}

func delta(epoch string, rev uint64, removed []unit.Unit, rows ...api.Row) api.RowsPage {
	return api.RowsPage{Epoch: epoch, Rev: rev, Rows: rows, Removed: removed}
}

func TestSetApplyIgnoresOlderPage(t *testing.T) {
	var s Set
	if s.Since() != (api.Since{}) {
		t.Fatalf("an empty set asks from %+v", s.Since())
	}
	if !s.Apply(full("e1", 5, row(1, "one"), row(2, "two"))) || s.Since() != (api.Since{Epoch: "e1", Rev: 5}) {
		t.Fatalf("full page not taken: %+v", s.Since())
	}
	if !s.Apply(delta("e1", 7, nil, row(1, "one again"))) {
		t.Fatal("a delta of the same epoch refused")
	}
	if !s.Apply(delta("e1", 6, []unit.Unit{row(2, "").Unit}, row(1, "stale"))) {
		t.Fatal("an older delta refused instead of ignored")
	}
	if got := titles(s.All()); !slices.Equal(got, []string{"one again", "two"}) || s.Since().Rev != 7 {
		t.Fatalf("after an older page: %v at %+v", got, s.Since())
	}
	if !s.Apply(delta("e1", 8, []unit.Unit{row(2, "").Unit})) {
		t.Fatal("a removal refused")
	}
	if got := titles(s.All()); !slices.Equal(got, []string{"one again"}) || s.Since().Rev != 8 {
		t.Fatalf("after a removal: %v at %+v", got, s.Since())
	}
}

func TestSetRefusesADeltaFromAnotherEpoch(t *testing.T) {
	var s Set
	if s.Apply(delta("e1", 3, nil, row(1, "one"))) {
		t.Fatal("an empty set took a delta")
	}
	s.Apply(full("e1", 5, row(1, "one"), row(2, "two")))
	if s.Apply(delta("e2", 1, nil, row(3, "three"))) {
		t.Fatal("a delta of another epoch was taken")
	}
	if got := titles(s.All()); !slices.Equal(got, []string{"one", "two"}) || s.Since() != (api.Since{Epoch: "e1", Rev: 5}) {
		t.Fatalf("a refused delta changed the set: %v at %+v", got, s.Since())
	}
	if !s.Apply(full("e2", 1, row(3, "three"))) {
		t.Fatal("a full page of the new epoch refused")
	}
	if got := titles(s.All()); !slices.Equal(got, []string{"three"}) || s.Since() != (api.Since{Epoch: "e2", Rev: 1}) {
		t.Fatalf("a full page of the new epoch: %v at %+v", got, s.Since())
	}
}

func TestSetListsByUnitAsTheDaemon(t *testing.T) {
	done := row(9, "done")
	done.Kind, done.Summary.Closed = "done", true
	other := row(4, "other")
	other.Views = []string{"threads"}
	rows := []api.Row{row(2, "two"), done, other, row(3, "three"), row(1, "one")}
	var s Set
	s.Apply(full("e1", 1, rows...))
	for name, got := range map[string][]api.Row{"all": s.All(), "mine": s.View("mine"), "threads": s.View("threads")} {
		want := map[string][]string{
			"all":     {"one", "two", "three", "other", "done"},
			"mine":    {"one", "two", "three"},
			"threads": {"other"},
		}[name]
		if !slices.Equal(titles(got), want) {
			t.Errorf("%s: %v, want %v", name, titles(got), want)
		}
	}
}
