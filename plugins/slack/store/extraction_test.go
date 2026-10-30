package store

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

func init() {
	reads = append(reads, "DB.Extraction", "ExtractionStore.States", "ExtractionStore.Watched", "ExtractionStore.Hints",
		"ExtractionStore.Day", "ExtractionStore.Migrated", "SegmentStore.Of", "SegmentStore.Rehoming")
	u := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1.1"}
	writes = append(writes,
		writeCase{method: "ExtractionStore.Save", call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Extraction().Save(ctx, ExtractionSave{States: map[unit.Unit]ExtractionState{u: {Brief: `{"goal":"b"}`, At: noteAt}},
				Hints: map[unit.Unit]map[string]ExtractionHint{u: {"e1": {Mode: "none"}}}})
		}, touches: true, units: []unit.Unit{u}},
		writeCase{method: "ExtractionStore.MarkClosed", call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Extraction().MarkClosed(ctx, u, noteAt)
		}, touches: true, units: []unit.Unit{u}},
		writeCase{method: "ExtractionStore.SetWatched", call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Extraction().SetWatched(ctx, u, true, noteAt)
		}},
		writeCase{method: "ExtractionStore.MarkMigrated", call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Extraction().MarkMigrated(ctx, "person-keys", noteAt)
		}},
		writeCase{method: "ExtractionStore.Spend", call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Extraction().Spend(ctx, "2026-10-22", 0.1)
		}},
		writeCase{method: "ExtractionStore.Record", call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Extraction().Record(ctx, ExtractionRun{Unit: u, At: noteAt, Outcome: "ok"})
		}},
	)
	hotPlans = append(hotPlans,
		hotPlan{name: "extraction states of units", query: extractionStatesSQL, args: 1,
			want: []string{"SEARCH extraction_cursors USING PRIMARY KEY (provider=? AND unit_key=?)"}},
		hotPlan{name: "watched extraction units", query: extractionWatchedSQL, args: 1,
			want: []string{"SEARCH extraction_cursors USING PRIMARY KEY (provider=?)"}},
		hotPlan{name: "extraction hints of a unit", query: extractionHintsSQL, args: 2,
			want: []string{"SEARCH extraction_hints USING PRIMARY KEY (provider=? AND unit_key=?)"}},
	)
}

func openExtraction(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestExtractionStoreRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openExtraction(t)
	s := db.Extraction()
	u := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000100"}
	other := unit.Unit{Provider: "slack", Key: "T000AAA/D000AAA/seg/1"}
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	want := ExtractionState{LastTS: "1700000000.000200", LastSeq: 7, Brief: `{"goal":"b","kind":"request"}`, Verdict: "v", VerdictWhy: "w", ViewerRole: "r",
		Topic: "t", StartedByViewer: true, ClosurePosted: true, At: at}
	if err := s.Save(ctx, ExtractionSave{States: map[unit.Unit]ExtractionState{u: want},
		Hints: map[unit.Unit]map[string]ExtractionHint{u: {"e1": {Mode: "session", Context: "@repo", Why: "y"}}}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.States(ctx, []unit.Unit{u, other})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[u] != want {
		t.Fatalf("states %+v", got)
	}
	if err := s.SetWatched(ctx, u, true, at); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, ExtractionSave{States: map[unit.Unit]ExtractionState{u: want},
		Hints: map[unit.Unit]map[string]ExtractionHint{u: {"e2": {Mode: "none"}}}}); err != nil {
		t.Fatal(err)
	}
	w, err := s.Watched(ctx, "slack")
	if err != nil || len(w) != 1 || !w[u].Watched || w[u].Brief != want.Brief {
		t.Fatalf("watched %+v %v", w, err)
	}
	h, err := s.Hints(ctx, u)
	if err != nil || len(h) != 2 || h["e2"].Mode != "none" || h["e1"].Context != "@repo" {
		t.Fatalf("hints %+v %v", h, err)
	}
	for range 2 {
		if err := s.Spend(ctx, "2026-10-22", 0.25); err != nil {
			t.Fatal(err)
		}
	}
	calls, cost, err := s.Day(ctx, "2026-10-22")
	if err != nil || calls != 2 || cost != 0.5 {
		t.Fatalf("day %d %v %v", calls, cost, err)
	}
	if calls, cost, err := s.Day(ctx, "2026-10-23"); err != nil || calls != 0 || cost != 0 {
		t.Fatalf("next day %d %v %v", calls, cost, err)
	}
	if err := s.Record(ctx, ExtractionRun{Unit: u, At: at, Outcome: "failed", Error: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSegmentsOfAConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openExtraction(t)
	seg := db.Segments()
	unitOf := func(team, conv, id string) unit.Unit {
		return unit.Unit{Provider: "slack", Key: team + "/" + conv + "/seg/" + id}
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	if _, err := seg.Apply(ctx, "slack", "T000AAA", "D000AAA", []SegmentSpan{{FromTS: "1700000000.000100", ToTS: "1700000000.000200"},
		{FromTS: "1700000000.000300"}}, unitOf, now); err != nil {
		t.Fatal(err)
	}
	if _, err := seg.Apply(ctx, "slack", "T000AAA", "D000AAA", []SegmentSpan{{FromTS: "1700000000.000300", ToTS: ""}}, unitOf, now); err != nil {
		t.Fatal(err)
	}
	got, err := seg.Of(ctx, "slack", "D000AAA")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].FromTS != "1700000000.000100" || got[1].FromTS != "1700000000.000300" {
		t.Fatalf("open segments %+v", got)
	}
}

func TestTheExtractionKeepsTheBriefItLastWrote(t *testing.T) {
	db := openExtraction(t)
	ctx := context.Background()
	u := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1.1"}
	day := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	brief := func(goal string) string { return `{"goal":"` + goal + `"}` }
	for i, c := range []struct {
		st   ExtractionState
		want string
	}{
		{ExtractionState{Brief: brief("first"), At: day}, brief("first")},
		{ExtractionState{Brief: brief("second"), At: day.Add(3 * time.Hour)}, brief("second")},
		{ExtractionState{At: day.Add(4 * time.Hour)}, brief("second")},
		{ExtractionState{Brief: brief("next day"), At: day.Add(24 * time.Hour)}, brief("next day")},
		{ExtractionState{At: day.Add(48 * time.Hour)}, brief("next day")},
	} {
		if err := db.Extraction().Save(ctx, ExtractionSave{States: map[unit.Unit]ExtractionState{u: c.st}}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		got, err := db.Extraction().States(ctx, []unit.Unit{u})
		if err != nil || got[u].Brief != c.want {
			t.Fatalf("after save %d the brief is %q, want %q (%v)", i, got[u].Brief, c.want, err)
		}
	}
}
