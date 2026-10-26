package inventory_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/domain/card"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// legacyRecentActivitySQL is the feed's query when it read pull request
// events only; the GitHub items must come out of the neutral query as
// they came out of this one.
const legacyRecentActivitySQL = `SELECT p.owner, p.repo, p.number, e.kind, e.key, e.actor, e.at, e.data, p.title, p.url, p.author, p.state
	FROM events e JOIN pull_requests p ON +e.provider = 'github' AND p.owner = substr(e.unit_key, 1, instr(e.unit_key, '/') - 1)
	 AND p.repo = substr(e.unit_key, instr(e.unit_key, '/') + 1, instr(e.unit_key, '#') - instr(e.unit_key, '/') - 1)
	 AND p.number = CAST(substr(e.unit_key, instr(e.unit_key, '#') + 1) AS INTEGER)
	WHERE e.kind NOT IN (MARKS) ORDER BY e.at DESC, e.kind, e.key LIMIT ?`

func legacyRecentActivity(t *testing.T, f *storetest.Fixture, limit int) []dinv.ActivityEvent {
	t.Helper()
	kinds := dinv.BookkeepingKinds()
	args := make([]any, 0, len(kinds)+1)
	for _, k := range kinds {
		args = append(args, k)
	}
	q := strings.Replace(legacyRecentActivitySQL, "MARKS", strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ","), 1)
	rows, err := f.DB.R().QueryContext(context.Background(), q, append(args, limit)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []dinv.ActivityEvent
	for rows.Next() {
		var a dinv.ActivityEvent
		var at string
		if err := rows.Scan(&a.PR.Owner, &a.PR.Repo, &a.PR.Number, &a.Kind, &a.Key, &a.Actor, &at, &a.Data, &a.Title, &a.URL, &a.Author, &a.State); err != nil {
			t.Fatal(err)
		}
		if a.At, err = time.Parse("2006-01-02T15:04:05.000000000Z", at); err != nil {
			t.Fatal(err)
		}
		a.Unit = a.PR.Unit()
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func saveCard(t *testing.T, f *storetest.Fixture, c card.Card) {
	t.Helper()
	if _, err := f.DB.Cards().Save(context.Background(), c, f.Now); err != nil {
		t.Fatal(err)
	}
}

func TestRecentActivityKeepsThePullRequestItemsOfTheSeededStore(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	ctx := context.Background()
	for _, r := range f.Open[:5] {
		saveCard(t, f, card.Card{Unit: r.Unit(), Title: "the card's title", State: "open", Link: "https://card.example/" + r.String(), StartedByMe: true})
	}
	chat := unit.Unit{Provider: "chat", Key: "C1/1.1"}
	saveCard(t, f, card.Card{Unit: chat, Title: "a thread", State: "thread", Link: "https://chat.example/C1/1.1"})
	sqlitetest.Exec(t, f.DB, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, 'turn_sent', 'k1', 'loop', ?, '')`,
		string(chat.Provider), chat.Key, f.Now.Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000000000Z"))

	const limit = 500
	want := legacyRecentActivity(t, f, limit)
	if len(want) < 100 {
		t.Fatalf("the seed holds %d pull request items", len(want))
	}
	got, err := f.Inventory.RecentActivity(ctx, limit+1)
	if err != nil {
		t.Fatal(err)
	}
	var prs []dinv.ActivityEvent
	for _, a := range got {
		if _, ok := pr.RefOf(a.Unit); ok {
			prs = append(prs, a)
		}
	}
	if !reflect.DeepEqual(prs, want) {
		g, _ := json.Marshal(prs[:min(3, len(prs))])
		w, _ := json.Marshal(want[:3])
		t.Fatalf("pull request items changed (%d, want %d):\n got %s\nwant %s", len(prs), len(want), g, w)
	}
}

func TestRecentActivityReadsEveryProviderFromItsCard(t *testing.T) {
	f := storetest.Empty(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ev := func(u unit.Unit, kind, key string, d time.Duration) {
		sqlitetest.Exec(t, f.DB, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES (?, ?, ?, ?, 'ana', ?, 'd')`,
			string(u.Provider), u.Key, kind, key, at.Add(d).UTC().Format("2006-01-02T15:04:05.000000000Z"))
	}
	chat := unit.Unit{Provider: "chat", Key: "C1/1.1"}
	uncarded := unit.Unit{Provider: "chat", Key: "C2/2.2"}
	saveCard(t, f, card.Card{Unit: chat, Title: "a thread", State: "thread", Link: "https://chat.example/C1/1.1", StartedByMe: true})
	ev(chat, "turn_sent", "k1", time.Minute)
	ev(chat, dinv.BookkeepingKinds()[0], "k2", 2*time.Minute)
	ev(uncarded, "turn_sent", "k3", 3*time.Minute)
	ev(unit.Unit{}, "outage", "k4", 4*time.Minute)

	got, err := f.Inventory.RecentActivity(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []dinv.ActivityEvent{{Unit: chat, Event: dinv.Event{Kind: "turn_sent", Key: "k1", Actor: "ana", At: at.Add(time.Minute), Data: "d"},
		Title: "a thread", URL: "https://chat.example/C1/1.1", State: "thread", Mine: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
