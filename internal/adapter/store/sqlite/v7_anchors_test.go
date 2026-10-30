package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestV7AnchorsKeepsTheEventsAndGivesThemNone(t *testing.T) {
	t.Parallel()
	path := v6Store(t,
		`INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES ('github', 'acme/api#1', 'review', 'r1', 'bruno', '2026-10-01T10:00:00.000000000Z', 'APPROVED:h1')`,
	)
	db := migrateV6(t, path)
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}
	evs, err := New(db).UnitEvents(context.Background(), []unit.Unit{u}, nil, time.Time{})
	if err != nil || len(evs) != 1 {
		t.Fatalf("events %+v %v", evs, err)
	}
	if e := evs[0]; e.Data != "APPROVED:h1" || e.Actor != "bruno" || e.Anchor != (unit.Anchor{}) {
		t.Errorf("event %+v", e)
	}
}
