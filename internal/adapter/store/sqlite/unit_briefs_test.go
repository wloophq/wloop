package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// TestAPluginsBriefsKeepOneRunPerUnitADay puts a hundred briefs of two
// units over one day and one the next: each unit keeps one run a day, the
// latest brief in use.
func TestAPluginsBriefsKeepOneRunPerUnitADay(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	day := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	us := []unit.Unit{{Provider: "acme", Key: "t1"}, {Provider: "acme", Key: "t2"}}
	for i := range 100 {
		for _, u := range us {
			if err := db.PutUnitBrief(ctx, u, "x1", fmt.Sprintf(`{"goal":"g%d"}`, i), day.Add(time.Duration(i)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.PutUnitBrief(ctx, us[0], "x2", `{"goal":"next"}`, day.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for u, want := range map[unit.Unit]int{us[0]: 2, us[1]: 1} {
		var n int
		if err := db.R().QueryRowContext(ctx, `SELECT count(*) FROM brief_runs WHERE provider = ? AND unit_key = ? AND trigger = 'plugin'`,
			string(u.Provider), u.Key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s keeps %d runs, want %d", u, n, want)
		}
	}
	var version, body string
	if err := db.R().QueryRowContext(ctx, `SELECT r.version, r.brief FROM unit_briefs b JOIN brief_runs r ON r.id = b.run
		WHERE b.provider = 'acme' AND b.unit_key = 't2'`).Scan(&version, &body); err != nil || version != "x1" || body != `{"goal":"g99"}` {
		t.Fatalf("t2 uses %q %s %v, want the last brief", version, body, err)
	}
}
