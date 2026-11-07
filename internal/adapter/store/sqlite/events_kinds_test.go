package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.EventsOfKinds")
	hotPlans = append(hotPlans, hotPlan{name: "events of kinds", query: eventsOfKindsSQL, args: 1,
		want: []string{"SEARCH events USING INDEX events_kind (kind=?)"}, tempSort: "USE TEMP B-TREE FOR ORDER BY"})
}

func TestEventsOfKindsAreEveryUnitsEventsOfThoseKindsOldestFirst(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	a, b := unit.Unit{Provider: "task", Key: "t-a"}, unit.Unit{Provider: "task", Key: "t-b"}
	at := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	if err := New(db).RecordEvents(ctx,
		port.Event{Unit: b, Kind: "verb_split", At: at, Data: "b"},
		port.Event{Unit: a, Kind: "verb_done", At: at.Add(time.Minute)},
		port.Event{Unit: a, Kind: "merge_record", At: at.Add(2 * time.Minute), Data: "a"},
	); err != nil {
		t.Fatal(err)
	}
	got, err := db.EventsOfKinds(ctx, []string{"merge_record", "verb_split"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Unit != b || got[0].Data != "b" || got[1].Unit != a || got[1].Kind != "merge_record" || !got[1].At.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("events %+v", got)
	}
}
