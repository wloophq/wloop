package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.Activities")
}

func TestActivitiesAggregatesTurnsUsageAndBriefRetriesOfOneProvider(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	chat := unit.ProviderID("chat")
	forge := unit.ProviderID("forge")
	turn := func(seq int, p unit.ProviderID, key, intent, state string) {
		exec(`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, state, sent_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, seq, string(p), key, intent, "r"+key+intent, state, at.Format(TimeLayout))
	}
	usage := func(seq int, model string, in, out int, cost any) {
		exec(`INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at) VALUES (?, ?, 1, ?, ?, 99, 99, ?, ?, ?)`,
			seq, model, in, out, cost, at.Format(TimeLayout), at.Format(TimeLayout))
	}
	turn(1, chat, "C1/1.1", "review", "done")
	turn(2, chat, "C1/1.1", "address", "started")
	turn(3, chat, "C1/1.1", "nudge", "asked")
	turn(4, chat, "C2/2.2", "probe", "failed")
	turn(5, forge, "C1/1.1", "review", "started")
	usage(1, "opus", 100, 20, 0.5)
	usage(1, "haiku", 10, 2, nil)
	usage(2, "opus", 30, 5, 0.25)
	usage(5, "opus", 1000, 1000, 9.0)
	retry := at.Add(time.Hour)
	exec(`INSERT INTO unit_briefs (provider, unit_key, retry_at) VALUES (?, 'C3/3.3', ?), (?, 'C4/4.4', ''), (?, 'C5/5.5', ?)`,
		string(chat), retry.Format(TimeLayout), string(chat), string(forge), retry.Format(TimeLayout))

	got, err := db.Activities(ctx, chat)
	if err != nil {
		t.Fatal(err)
	}
	u := func(k string) unit.Unit { return unit.Unit{Provider: chat, Key: k} }
	if a := got[u("C1/1.1")]; a.Turns != 3 || a.OpenTurns != 2 || a.LastIntent != "nudge" || a.Tokens != 167 || a.CostUSD != 0.75 || !a.BriefRetryAt.IsZero() {
		t.Fatalf("C1: %+v", a)
	}
	if a := got[u("C2/2.2")]; a.Turns != 1 || a.OpenTurns != 0 || a.LastIntent != "probe" || a.Tokens != 0 || a.CostUSD != 0 {
		t.Fatalf("C2: %+v", a)
	}
	if a := got[u("C3/3.3")]; a.Turns != 0 || !a.BriefRetryAt.Equal(retry) {
		t.Fatalf("C3: %+v", a)
	}
	if _, ok := got[u("C4/4.4")]; ok || len(got) != 3 {
		t.Fatalf("another provider's or an idle unit's activity: %+v", got)
	}
}
