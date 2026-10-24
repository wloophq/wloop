package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
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
