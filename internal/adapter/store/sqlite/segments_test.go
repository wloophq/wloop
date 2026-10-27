package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func segUnit(team, conv, id string) unit.Unit {
	return unit.Unit{Provider: "slack", Key: team + "/" + conv + "/seg/" + id}
}

func init() {
	reads = append(reads, "DB.Segments", "SegmentStore.Covering", "SegmentStore.Segment")
	writes = append(writes, writeCase{
		method: "SegmentStore.Apply",
		seed: func(t *testing.T, e *noteEnv) {
			if _, err := e.db.Segments().Apply(context.Background(), "slack", "T000AAA", "D000AAA",
				[]SegmentSpan{{FromTS: "1.1", ToTS: "1.5"}, {FromTS: "1.6"}}, segUnit, noteAt); err != nil {
				t.Fatal(err)
			}
		},
		call: func(ctx context.Context, e *noteEnv) error {
			_, err := e.db.Segments().Apply(ctx, "slack", "T000AAA", "D000AAA",
				[]SegmentSpan{{ID: "11", FromTS: "1.1", ToTS: "1.5"}, {ID: "16", FromTS: "1.6", ToTS: "1.8"}, {FromTS: "1.9"}}, segUnit, noteAt)
			return err
		},
		topics: port.TopicSourceEvents, units: []unit.Unit{segUnit("T000AAA", "D000AAA", "16"), segUnit("T000AAA", "D000AAA", "19")},
	})
	hotPlans = append(hotPlans,
		hotPlan{name: "segments of a conversation", query: segmentsOfConvSQL, args: 2,
			want: []string{"SEARCH segment_map USING PRIMARY KEY (provider=? AND conversation=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "segments covering a ts", query: segmentsCovering, args: 4,
			want: []string{"SEARCH segment_map USING PRIMARY KEY (provider=? AND conversation=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "drafts on the units of a conversation", query: draftsOfConvSQL, args: 3,
			want: []string{"SEARCH drafts USING COVERING INDEX drafts_unit (provider=? AND unit_key>? AND unit_key<?)"}},
		hotPlan{name: "a segment", query: segmentByIDSQL, args: 3,
			want: []string{"SEARCH segment_map USING PRIMARY KEY (provider=? AND conversation=? AND segment_id=?)"}},
	)
}

func BenchmarkSegments300(b *testing.B) {
	db, err := Open(filepath.Join(b.TempDir(), "seg.db"), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	spans := make([]SegmentSpan, 300)
	for i := range spans {
		spans[i] = SegmentSpan{FromTS: fmt.Sprintf("1%09d.000000", i*10), ToTS: fmt.Sprintf("1%09d.000000", i*10+5)}
	}
	spans[len(spans)-1].ToTS = ""
	got, err := db.Segments().Apply(context.Background(), "slack", "T000AAA", "D000AAA", spans, segUnit, noteAt)
	if err != nil {
		b.Fatal(err)
	}
	for i := range spans {
		spans[i].ID = got[i].ID
	}
	b.Run("Covering", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			if _, err := db.Segments().Covering(context.Background(), "slack", "D000AAA", fmt.Sprintf("1%09d.000000", (i%300)*10+2)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Apply", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			spans[len(spans)-2].ToTS = fmt.Sprintf("1%09d.00000%d", 2980, i%2)
			if _, err := db.Segments().Apply(context.Background(), "slack", "T000AAA", "D000AAA", spans, segUnit, noteAt); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestASegmentTakesTheDraftsRecordedInItsRangeBeforeIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	place := func(conv, ts string) unit.Unit {
		return unit.Unit{Provider: "slack", Key: "T000AAA/" + conv + "/" + ts}
	}
	var ids []draft.ID
	for _, u := range []unit.Unit{place("D000AAA", "1700000000.000200"), place("D000AAA", "1700000009.000100"), place("D000BBB", "1700000000.000200"),
		segUnit("T000AAA", "D000AAA", "x")} {
		d, err := db.Drafts().Insert(ctx, draft.Draft{Unit: u, Kind: draft.Reply, Text: "hi", Origin: draft.Origin{Kind: draft.OriginSession},
			State: draft.Sent, CreatedAt: noteAt, UpdatedAt: noteAt})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	rows, err := db.Segments().Apply(ctx, "slack", "T000AAA", "D000AAA",
		[]SegmentSpan{{FromTS: "1700000000.000100", ToTS: "1700000005.000000"}, {FromTS: "1700000006.000000"}}, segUnit, noteAt)
	if err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{segUnit("T000AAA", "D000AAA", rows[0].ID), segUnit("T000AAA", "D000AAA", rows[1].ID), place("D000BBB", "1700000000.000200"),
		segUnit("T000AAA", "D000AAA", "x")}
	for i, id := range ids {
		d, err := db.Drafts().Get(ctx, id)
		if err != nil || d.Unit != want[i] {
			t.Errorf("draft %d on %v, want %v (%v)", id, d.Unit, want[i], err)
		}
	}
}
