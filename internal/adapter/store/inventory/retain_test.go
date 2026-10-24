package inventory

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

const day = 24 * time.Hour

func TestRetainDropsOrphanParts(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	parts := func() int {
		var n int
		if err := s.db.R().QueryRowContext(ctx, `SELECT count(*) FROM instruction_parts`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	old := "intro\n# One\nsame\n## Two\nfirst"
	kept := "intro\n# One\nsame\n## Two\nsecond"
	if err := s.SaveTurn(ctx, dinv.StoredTurn{ID: "old000000000", Kind: "review", Repo: "acme/api", Text: old}, now.Add(-31*day)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTurn(ctx, dinv.StoredTurn{ID: "new000000000", Kind: "review", Repo: "acme/api", Text: kept}, now.Add(-29*day)); err != nil {
		t.Fatal(err)
	}
	if got := parts(); got != 4 {
		t.Fatalf("%d parts stored, want 4", got)
	}
	got, err := s.db.Retain(ctx, now, sqlite.Retention{Instructions: 30 * day})
	if err != nil {
		t.Fatal(err)
	}
	if got["turn_instructions"] != 1 || got["instruction_parts"] != 1 {
		t.Errorf("retained %v, want one turn and its one unshared part", got)
	}
	if n := parts(); n != 3 {
		t.Errorf("%d parts left, want 3", n)
	}
	if _, ok, err := s.Turn(ctx, "old000000000"); ok || err != nil {
		t.Errorf("the old turn reads: %v %v", ok, err)
	}
	if st, ok, err := s.Turn(ctx, "new000000000"); !ok || err != nil || st.Text != kept {
		t.Errorf("the kept turn reads %q %v %v", st.Text, ok, err)
	}
}

func TestTimingsMaxWindowIntact(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	gone := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	kept := github.Ref{Owner: "acme", Repo: "api", Number: 2}
	active := github.Ref{Owner: "acme", Repo: "api", Number: 3}
	for ref, ended := range map[github.Ref]time.Time{gone: now.Add(-401 * day), kept: now.Add(-399 * day)} {
		upsert(t, s, dinv.PR{Ref: ref, Title: "T", Author: "ana", State: "MERGED", CreatedAt: ended.Add(-day), MergedAt: ended, ClosedAt: ended})
		if _, err := s.AppendEvents(ctx, []dinv.Event{
			{PR: ref, Kind: dinv.KindOpened, Actor: "ana", At: ended.Add(-day)},
			{PR: ref, Kind: dinv.KindMerged, Actor: "bob", At: ended},
		}); err != nil {
			t.Fatal(err)
		}
		sqlitetest.Exec(t, s.db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, ended_at) VALUES ('github', ?, 'review', 'r', 'done', ?, ?)`,
			ref.String(), ts(ended.Add(-time.Hour)), ts(ended.Add(-time.Minute)))
		id, err := s.StartBriefRun(ctx, dinv.BriefRun{PR: ref, Trigger: "wanted", StartedAt: ended.Add(-2 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.EndBriefRun(ctx, dinv.BriefRun{ID: id, PR: ref, Trigger: "wanted", StartedAt: ended.Add(-2 * time.Hour), EndedAt: ended.Add(-time.Hour), Outcome: "ok", Tags: []string{"change:fix", "area:old"}, Files: []string{"old.go"}}, dinv.BriefRecord{PR: ref}); err != nil {
			t.Fatal(err)
		}
	}
	in := now.Add(-100 * day)
	upsert(t, s, dinv.PR{Ref: active, Title: "T", Author: "ana", State: "OPEN", CreatedAt: in})
	if _, err := s.AppendEvents(ctx, []dinv.Event{
		{PR: active, Kind: dinv.KindOpened, Actor: "ana", At: in},
		{PR: active, Kind: dinv.KindReview, Key: "r1", Actor: "me", Data: "APPROVED:c1", At: in.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	sqlitetest.Exec(t, s.db, `INSERT INTO turns (provider, unit_key, intent, turn_id, state, sent_at, started_at, ended_at, agent) VALUES ('github', ?, 'review', 'a', 'done', ?, ?, ?, 'claude')`,
		active.String(), ts(in), ts(in.Add(time.Minute)), ts(in.Add(10*time.Minute)))
	id, err := s.StartBriefRun(ctx, dinv.BriefRun{PR: active, Trigger: "wanted", StartedAt: in})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndBriefRun(ctx, dinv.BriefRun{ID: id, PR: active, Trigger: "wanted", StartedAt: in, EndedAt: in.Add(13 * time.Second), Outcome: "ok", Tags: []string{"change:fix"}, Files: []string{"api/export.go"}}, dinv.BriefRecord{PR: active}); err != nil {
		t.Fatal(err)
	}

	since := now.Add(-365 * day)
	before, err := s.Timings(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.db.Retain(ctx, now, sqlite.DefaultRetention)
	if err != nil {
		t.Fatal(err)
	}
	if got["pull_requests"] != 1 || got["turns"] != 1 || got["pr_tags"] != 2 {
		t.Fatalf("retained %v, want the pull request ended 401 days ago", got)
	}
	after, err := s.Timings(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the 365-day timings changed:\nbefore %+v\nafter  %+v", before, after)
	}
}
