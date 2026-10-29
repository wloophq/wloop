package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/event"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

func TestRowFactsCarryChecksTurnsUsageAndWhatComesNext(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	created := fixtureNow.Add(-72 * time.Hour)
	f.open(7, "ana", fixtureNow.Add(-time.Hour), func(pr *dinv.PR) {
		pr.CreatedAt, pr.ChangedFiles, pr.ChecksState = created, 12, "FAILURE"
		pr.FailingChecks = []dinv.FailingCheck{{Name: "lint"}, {Name: "test (ubuntu)", URL: "https://ci/1"}}
	})
	f.open(8, "bob", fixtureNow.Add(-time.Hour), nil)
	f.open(9, "carl", fixtureNow.Add(-time.Hour), nil)
	exec := func(q string, args ...any) {
		t.Helper()
		sqlitetest.Exec(t, f.db, q, args...)
	}
	sent := storeTime(fixtureNow.Add(-30 * time.Minute))
	exec(`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, state, sent_at) VALUES
		(1, 'github', 'acme/api#7', 'review', 'a', 'done', ?), (2, 'github', 'acme/api#7', 'address', 'b', 'started', ?)`, sent, sent)
	exec(`INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, first_at, last_at) VALUES
		(1, 'opus', 1, 1000, 200, 5000, 0, 0.42, ?, ?), (2, 'opus', 1, 300, 50, 0, 0, NULL, ?, ?)`, sent, sent, sent, sent)
	unmute, retry := fixtureNow.Add(2*time.Hour), fixtureNow.Add(time.Hour)
	if err := inventorytest.Of(f.st).SetOverride(ctx, dinv.Override{PR: ref(7), MutedUntil: unmute}); err != nil {
		t.Fatal(err)
	}
	if err := inventorytest.Of(f.st).SetOverride(ctx, dinv.Override{PR: ref(8), MutedUntil: fixtureNow.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO unit_briefs (provider, unit_key, retry_at) VALUES ('github', 'acme/api#7', ?), ('github', 'acme/api#8', ?)`,
		storeTime(retry), storeTime(fixtureNow.Add(-time.Hour)))

	byNumber := map[int]*api.RowFacts{}
	for _, r := range f.rows("?view=all") {
		byNumber[rowNumber(r)] = payload(r).Facts
	}
	got := byNumber[7]
	if got == nil || !got.CreatedAt.Equal(created) || got.ChangedFiles != 12 || got.Checks.State != "FAILURE" ||
		!slices.Equal(got.Checks.Failing, []string{"lint", "test (ubuntu)"}) {
		t.Fatalf("7: %+v", got)
	}
	if want := (api.TurnFacts{Count: 2, Open: 1, LastIntent: "address", Tokens: 1550, CostUSD: 0.42}); got.Turns != want {
		t.Fatalf("7 turns: %+v", got.Turns)
	}
	if want := []api.Upcoming{{At: retry, What: api.UpcomingBriefRetry}, {At: unmute, What: api.UpcomingUnmute}}; !slices.EqualFunc(got.Next, want, sameUpcoming) {
		t.Fatalf("7 next: %+v", got.Next)
	}
	if got := byNumber[8]; got == nil || got.Next != nil {
		t.Fatalf("8: a past unmute and retry are not next: %+v", got)
	}
	if got := byNumber[9]; got == nil || got.Turns != (api.TurnFacts{}) || got.Checks.State != "SUCCESS" || got.Checks.Failing != nil || got.Next != nil || got.ChangedFiles != 0 {
		t.Fatalf("9: %+v", got)
	}
	raw, _ := json.Marshal(byNumber[7])
	t.Logf("%s", raw)
	raw, _ = json.Marshal(byNumber[9])
	t.Logf("%s", raw)
}

func storeTime(t time.Time) string { return t.UTC().Format(event.TimeLayout) }

func sameUpcoming(a, b api.Upcoming) bool { return a.What == b.What && a.At.Equal(b.At) }

// payload is a GitHub row's payload, zero for another row.
func payload(r api.Row) prrow.Payload {
	p, _ := prrow.PayloadOf(r)
	return p
}
