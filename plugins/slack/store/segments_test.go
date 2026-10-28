package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/unit"
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
		touches: true, units: []unit.Unit{segUnit("T000AAA", "D000AAA", "16"), segUnit("T000AAA", "D000AAA", "19")},
	})
	hotPlans = append(hotPlans,
		hotPlan{name: "segments of a conversation", query: segmentsOfConvSQL, args: 2,
			want: []string{"SEARCH segment_map USING PRIMARY KEY (provider=? AND conversation=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "segments covering a ts", query: segmentsCovering, args: 4,
			want: []string{"SEARCH segment_map USING PRIMARY KEY (provider=? AND conversation=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "messages of a conversation from a ts", query: messagesFromSQL, args: 2,
			want: []string{"SEARCH slack_messages USING PRIMARY KEY (conversation=? AND ts>?)"}},
		hotPlan{name: "a segment", query: segmentByIDSQL, args: 3,
			want: []string{"SEARCH segment_map USING PRIMARY KEY (provider=? AND conversation=? AND segment_id=?)"}},
	)
}

func BenchmarkSegments300(b *testing.B) {
	db, err := Open(context.Background(), b.TempDir())
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

// saveDM stores messages at tss in the DM conv of team T000AAA.
func saveDM(t *testing.T, db *DB, conv string, tss ...string) {
	t.Helper()
	msgs := make([]MessageRow, len(tss))
	for i, ts := range tss {
		msgs[i] = MessageRow{TS: ts, User: "U0001", Text: "text"}
	}
	if _, err := db.Slack().Save(context.Background(), FactsRows{Team: "T000AAA", Objects: []ObjectFacts{{
		Object:   ObjectRow{Object: conv, Conversation: conv, Kind: SlackDM, Version: "1"},
		Messages: msgs,
	}}}, noteAt); err != nil {
		t.Fatal(err)
	}
}

// draftHome is the host's drafts as the broker moves them: it records the
// moves it is asked for and applies them to the unit of each draft.
type draftHome struct {
	moves  [][]src.UnitMove
	drafts map[int]unit.Unit
}

func (h *draftHome) RehomeDrafts(_ context.Context, moves []src.UnitMove) (int, error) {
	h.moves = append(h.moves, moves)
	n := 0
	for _, m := range moves {
		for id, u := range h.drafts {
			if u.Wire() == m.From {
				h.drafts[id] = unit.Of(m.To)
				n++
			}
		}
	}
	return n, nil
}

func TestRehomeDraftsGoesThroughTheBroker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	saveDM(t, db, "D000AAA", "1700000000.000200", "1700000009.000100", "1699999999.000100")
	saveDM(t, db, "D000BBB", "1700000000.000300")
	home := &draftHome{}
	rows, err := db.Segments().Rehoming(home).Apply(ctx, "slack", "T000AAA", "D000AAA",
		[]SegmentSpan{{FromTS: "1700000000.000100", ToTS: "1700000005.000000"}, {FromTS: "1700000006.000000"}}, segUnit, noteAt)
	if err != nil {
		t.Fatal(err)
	}
	place := func(ts string) plugin.Unit { return plugin.Unit{Provider: "slack", Key: "T000AAA/D000AAA/" + ts} }
	seg := func(id string) plugin.Unit {
		u := segUnit("T000AAA", "D000AAA", id)
		return plugin.Unit{Provider: string(u.Provider), Key: u.Key}
	}
	want := [][]src.UnitMove{{{From: place("1700000000.000200"), To: seg(rows[0].ID)}, {From: place("1700000009.000100"), To: seg(rows[1].ID)}}}
	if !reflect.DeepEqual(home.moves, want) {
		t.Errorf("asked the broker for %+v, want %+v", home.moves, want)
	}
	if _, err := db.Segments().Apply(ctx, "slack", "T000AAA", "D000BBB", []SegmentSpan{{FromTS: "1700000000.000100"}}, segUnit, noteAt); err != nil {
		t.Fatal(err)
	}
}

func TestASegmentTakesTheDraftsRecordedInItsRangeBeforeIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTemp(t)
	saveDM(t, db, "D000AAA", "1700000000.000200", "1700000009.000100")
	saveDM(t, db, "D000BBB", "1700000000.000200")
	place := func(conv, ts string) unit.Unit {
		return unit.Unit{Provider: "slack", Key: "T000AAA/" + conv + "/" + ts}
	}
	home := &draftHome{drafts: map[int]unit.Unit{}}
	for i, u := range []unit.Unit{place("D000AAA", "1700000000.000200"), place("D000AAA", "1700000009.000100"), place("D000BBB", "1700000000.000200"),
		segUnit("T000AAA", "D000AAA", "x")} {
		home.drafts[i] = u
	}
	rows, err := db.Segments().Rehoming(home).Apply(ctx, "slack", "T000AAA", "D000AAA",
		[]SegmentSpan{{FromTS: "1700000000.000100", ToTS: "1700000005.000000"}, {FromTS: "1700000006.000000"}}, segUnit, noteAt)
	if err != nil {
		t.Fatal(err)
	}
	want := []unit.Unit{segUnit("T000AAA", "D000AAA", rows[0].ID), segUnit("T000AAA", "D000AAA", rows[1].ID), place("D000BBB", "1700000000.000200"),
		segUnit("T000AAA", "D000AAA", "x")}
	for i := range want {
		if got := home.drafts[i]; got != want[i] {
			t.Errorf("draft %d on %v, want %v", i, got, want[i])
		}
	}
}
