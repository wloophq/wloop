package inventory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/github"
)

func TestActivitiesAggregatesTurnsUsageAndBriefRetries(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	exec := func(q string, args ...any) {
		t.Helper()
		sqlitetest.Exec(t, s.db, q, args...)
	}
	turn := func(seq, number int, intent, state string) {
		exec(`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, state, sent_at) VALUES (?, 'github', ?, ?, ?, ?, ?)`, seq, fmt.Sprint("acme/api#", number), intent, "r"+intent, state, ts(at))
	}
	usage := func(seq int, model string, in, out int, cost any) {
		exec(`INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at) VALUES (?, ?, 1, ?, ?, 99, 99, ?, ?, ?)`, seq, model, in, out, cost, ts(at), ts(at))
	}
	turn(1, 7, "review", "done")
	turn(2, 7, "address", "started")
	turn(3, 7, "nudge", "asked")
	turn(4, 8, "probe", "failed")
	usage(1, "opus", 100, 20, 0.5)
	usage(1, "haiku", 10, 2, nil)
	usage(2, "opus", 30, 5, 0.25)
	retry := at.Add(time.Hour)
	exec(`INSERT INTO unit_briefs (provider, unit_key, retry_at) VALUES ('github', 'acme/api#9', ?), ('github', 'acme/api#10', '')`, ts(retry))

	got, err := s.Activities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := func(n int) github.Ref { return github.Ref{Owner: "acme", Repo: "api", Number: n} }
	if a := got[r(7)]; a.Turns != 3 || a.OpenTurns != 2 || a.LastIntent != "nudge" || a.Tokens != 167 || a.CostUSD != 0.75 || !a.BriefRetryAt.IsZero() {
		t.Fatalf("7: %+v", a)
	}
	if a := got[r(8)]; a.Turns != 1 || a.OpenTurns != 0 || a.LastIntent != "probe" || a.Tokens != 0 || a.CostUSD != 0 {
		t.Fatalf("8: %+v", a)
	}
	if a := got[r(9)]; a.Turns != 0 || !a.BriefRetryAt.Equal(retry) {
		t.Fatalf("9: %+v", a)
	}
	if _, ok := got[r(10)]; ok || len(got) != 3 {
		t.Fatalf("%+v", got)
	}
}
