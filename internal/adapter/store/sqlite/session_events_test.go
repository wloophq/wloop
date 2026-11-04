package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAnEventKeepsTheSessionAnAgentActedThrough(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	u := task.UnitOf("t-0000000000000001")
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	if err := New(db).RecordEvents(ctx,
		port.Event{Unit: u, Kind: "verb_next", Key: "1", Actor: "agent:cockpit", Session: "s-user", At: at},
		port.Event{Unit: u, Kind: "verb_next", Actor: "person:cockpit", At: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	evs, err := db.EventsOf(ctx, []unit.Unit{u}, timeline.Filter{})
	if err != nil || len(evs) != 2 {
		t.Fatalf("events %+v %v", evs, err)
	}
	if evs[0].Session != "" || evs[1].Session != "s-user" {
		t.Fatalf("sessions %q %q, want none then s-user", evs[0].Session, evs[1].Session)
	}
}
