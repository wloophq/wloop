package sqlite_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestRetainBudget(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	old := f.Now.Add(-40 * 24 * time.Hour).Format(event.TimeLayout)
	for i := range 300 {
		sqlitetest.Exec(t, f.DB, `INSERT INTO events (provider, unit_key, kind, key, at) VALUES ('session', ?, 'session_idle', ?, ?)`,
			fmt.Sprint("term_old", i%30), fmt.Sprintf("term_old%d:%d", i%30, i), old)
		sqlitetest.Exec(t, f.DB, `INSERT INTO turn_stops (session, prompt_id, at) VALUES (?, ?, ?)`, fmt.Sprint("term_old", i%30), fmt.Sprint("p", i), old)
	}
	ctx, sc := metertest.Start(t, context.Background())
	got, err := f.DB.Retain(ctx, f.Now, sqlite.DefaultRetention)
	if err != nil {
		t.Fatal(err)
	}
	if got[sqlite.SessionEventsKey] < 300 || got["turn_stops"] != 300 {
		t.Fatalf("retained %v", got)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 20, {Res: meter.Tx}: 1})
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "write"}); n != 1 {
		t.Errorf("%d write transactions, want 1\n%s", n, metertest.Report(sc.Tally()))
	}
	t.Logf("retained %v in %d statements", got, metertest.Count(sc, metertest.Limit{Res: meter.SQL}))
}
