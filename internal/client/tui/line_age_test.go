package tui

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
)

func TestTheTerminalShowsALabeledAgeAsItsAge(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	specs := []api.FacetSpec{{ID: "owed", Label: "Owed", Sort: "time", Show: "age", ShowLabel: "since {{.at}}"}}
	words := api.RowWords{AgeNow: "now", AgeMinutes: "{{.m}}m", AgeHours: "{{.h}}h {{.m}}m", AgeDays: "{{.d}}d {{.h}}h"}
	it := lens.Item{Facets: map[string][]string{"owed": {"2026-11-07T09:00:00Z"}}}
	parts := compactAge(lens.Parts(it, specs, api.LensWords{RowWords: words}, now), words, now)
	if got := labels(parts["age"], " "); got != "3h 0m" {
		t.Errorf("age column %q, want the age alone", got)
	}
}
