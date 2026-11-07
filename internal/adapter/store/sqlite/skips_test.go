package sqlite

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestSkipStoreRoundTrips(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	a, b := unit.Unit{Provider: "acme", Key: "C1/1"}, unit.Unit{Provider: "acme", Key: "C1/2"}
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, s := range []struct {
		wf string
		u  unit.Unit
	}{{"nudge", a}, {"closure", a}, {"nudge", b}, {"nudge", a}} {
		if err := db.SaveSkip(ctx, s.wf, s.u, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DeleteSkip(ctx, "nudge", b); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSkip(ctx, "never", b); err != nil {
		t.Fatalf("deleting no skip: %v", err)
	}
	got, err := db.AllSkips(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[unit.Unit][]string{a: {"closure", "nudge"}}; !maps.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("skips %v, want %v", got, want)
	}
	if err := db.SaveSkip(ctx, "", a, at); err == nil {
		t.Error("a skip of no workflow was saved")
	}
}
