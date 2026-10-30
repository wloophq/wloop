package inventorytest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

func TestEveryEventWriterStoresAHandleKeyedEventUnderItsSession(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	if _, err := s.AppendEvents(ctx, []dinv.Event{
		{Kind: "session_idle", Key: "term_1:x", At: at},
		{Kind: "paused", At: at.Add(time.Second)},
	}); err != nil {
		t.Fatal(err)
	}
	var provider, key string
	if err := s.db.R().QueryRowContext(ctx, `SELECT provider, unit_key FROM events WHERE kind = 'session_idle'`).Scan(&provider, &key); err != nil || provider != "session" || key != "term_1" {
		t.Errorf("session_idle stored under (%q, %q) %v", provider, key, err)
	}
	evs, err := s.UnitlessEvents(ctx)
	if err != nil || len(evs) != 2 || evs[0].Kind != "session_idle" || evs[1].Kind != "paused" {
		t.Errorf("unitless events %+v %v", evs, err)
	}
}

func TestLoopTablesStoreANonGitHubUnit(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	if err := s.UpsertPR(ctx, dinv.PR{Ref: ref, Title: "t", State: dgh.StateOpen, LastSeenAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride(ctx, dinv.Override{PR: ref, Hold: true, Tracked: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEvent(ctx, dinv.Event{PR: ref, Kind: dinv.KindReview, Key: "r1", At: at}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO overrides (provider, unit_key, hold, tracked) VALUES ('thread', 'C1 thread 9', 1, 1)`,
		`INSERT INTO events (provider, unit_key, kind, key, at) VALUES ('thread', 'C1 thread 9', 'review', 'r1', '` + ts(at.Add(time.Minute)) + `')`,
		`INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at) VALUES ('thread', 'C1 thread 9', 'review', 'r9', 'sent', '` + ts(at) + `')`,
	} {
		sqlitetest.Exec(t, s.db, q)
	}

	overrides, err := s.Overrides(ctx)
	if err != nil || len(overrides) != 1 || !overrides[ref].Hold {
		t.Errorf("overrides %+v %v", overrides, err)
	}
	if tracked, err := s.Tracked(ctx); err != nil || len(tracked) != 1 || tracked[0] != ref {
		t.Errorf("tracked %v %v", tracked, err)
	}
	if o, err := s.Override(ctx, ref); err != nil || !o.Hold || o.PR != ref {
		t.Errorf("override %+v %v", o, err)
	}
	evs, err := s.Events(ctx, ref)
	if err != nil || !slices.ContainsFunc(evs, func(e dinv.Event) bool { return e.Kind == dinv.KindReview && e.At.Equal(at) }) {
		t.Errorf("events %+v %v", evs, err)
	}
	for _, e := range evs {
		if e.PR != ref {
			t.Errorf("event of %v read as the pull request's", e.PR)
		}
	}
	feed, err := s.RecentActivity(ctx, 10)
	if err != nil || len(feed) != 1 || feed[0].PR != ref || feed[0].Title != "t" {
		t.Errorf("activity %+v %v", feed, err)
	}
	if last, ok, err := s.LastEvent(ctx, ref, dinv.KindReview); err != nil || !ok || !last.At.Equal(at) {
		t.Errorf("last %+v %v %v", last, ok, err)
	}
}
