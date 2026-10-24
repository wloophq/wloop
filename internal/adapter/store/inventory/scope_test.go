package inventory

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func scopeEvents(t *testing.T, s *Store, refs ...github.Ref) []string {
	t.Helper()
	var out []string
	for _, r := range refs {
		evs, err := s.Events(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.Kind == dinv.KindFollowed || e.Kind == dinv.KindUnfollowed {
				out = append(out, r.String()+" "+e.Kind+" "+e.Data+" "+e.At.Format(time.TimeOnly))
			}
		}
	}
	return out
}

func TestFollowedEventsOnScopeChange(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := runAt
	s.clock = func() time.Time { now = now.Add(time.Minute); return now }
	other := github.Ref{Owner: "acme", Repo: "web", Number: 2}
	for _, r := range []github.Ref{ref, other} {
		if err := s.UpsertPR(ctx, dinv.PR{Ref: r, State: "OPEN"}); err != nil {
			t.Fatal(err)
		}
	}
	steps := []func() error{
		func() error { return s.SetReasons(ctx, ref, []string{"author", "repo"}) },
		func() error { return s.SetReasons(ctx, ref, []string{"author"}) },
		func() error { return s.SetReasons(ctx, ref, nil) },
		func() error { return s.SetWorkspace(ctx, ref, dinv.Workspace{ID: "ws1"}) },
		func() error { return s.ReplaceWorkspaces(ctx, map[github.Ref]dinv.Workspace{other: {ID: "ws2"}}) },
		func() error { return s.SetWorkspace(ctx, other, dinv.Workspace{ID: "ws3"}) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"acme/api#1 followed author,repo 12:01:00",
		"acme/api#1 unfollowed author 12:02:00",
		"acme/api#1 followed  12:03:00",
		"acme/api#1 unfollowed  12:04:00",
		"acme/web#2 followed  12:04:00",
	}
	if got := scopeEvents(t, s, ref, other); !slices.Equal(got, want) {
		t.Fatalf("events:\n%q", got)
	}
}

func situationKeys(t *testing.T, s *Store, ref github.Ref) []string {
	t.Helper()
	evs, err := s.Events(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.Kind == dinv.KindSituation {
			out = append(out, e.Data+" "+e.Key)
		}
	}
	slices.Sort(out)
	return out
}

func TestSituationEventOnlyOnChange(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for i, data := range []string{"review:me", "review:me", "review:author", "review:me"} {
		if err := s.RecordSituations(ctx, []dinv.Situation{{PR: ref, Data: data, At: runAt.Add(time.Duration(i) * time.Minute)}}); err != nil {
			t.Fatalf("%d %s: %v", i, data, err)
		}
	}
	want := []string{"review:author 2", "review:me 1", "review:me 3"}
	if got := situationKeys(t, s, ref); !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestSituationsFollowTheirNumberNotTheClock(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for i, c := range []struct {
		data string
		at   time.Time
	}{
		{"review:me", runAt},
		{"review:author", runAt},
		{"review:author", runAt.Add(-time.Hour)},
		{"review:me", runAt.Add(-time.Hour)},
	} {
		if err := s.RecordSituations(ctx, []dinv.Situation{{PR: ref, Data: c.data, At: c.at}}); err != nil {
			t.Fatalf("%d %s: %v", i, c.data, err)
		}
	}
	evs, err := s.Events(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, e := range evs {
		keys[e.Key] = e.Data
	}
	if want := map[string]string{"1": "review:me", "2": "review:author", "3": "review:me"}; !maps.Equal(keys, want) {
		t.Fatalf("%v", keys)
	}
}

func TestRecordSituationsInOneTx(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	refs := []github.Ref{ref, {Owner: "acme", Repo: "api", Number: 2}, {Owner: "acme", Repo: "api", Number: 3}}
	var sits []dinv.Situation
	for _, r := range refs {
		sits = append(sits, dinv.Situation{PR: r, Data: "review:me", At: runAt})
	}
	mctx, sc := metertest.Start(t, ctx)
	if err := s.RecordSituations(mctx, sits); err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.Tx}: 1, {Res: meter.SQL, Op: "exec"}: 3})
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "write"}); n != 1 {
		t.Fatalf("%d write transactions for 3 situations, want 1", n)
	}
	for _, r := range refs {
		if got := situationKeys(t, s, r); !slices.Equal(got, []string{"review:me 1"}) {
			t.Fatalf("%s: %q", r, got)
		}
	}
}

func TestRecordSituationsKeepsTheKey(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.RecordSituations(ctx, []dinv.Situation{{PR: ref, Data: "review:me", At: runAt}, {PR: ref, Data: "review:author", At: runAt}}); err != nil {
		t.Fatal(err)
	}
	if got := situationKeys(t, s, ref); !slices.Equal(got, []string{"review:author 2", "review:me 1"}) {
		t.Fatalf("one batch with two situations of a pull request: %q", got)
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES ('github', ?, ?, '2', '', ?, 'review:me')`,
			ref.String(), dinv.KindSituation, ts(runAt))
		return err
	})
	if err == nil {
		t.Fatal("the unique index took a second situation keyed 2")
	}
}

func TestFollowedEventsOnUpsertAndTrack(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	s.UseClock(func() time.Time { return runAt.Add(time.Hour) })
	other := github.Ref{Owner: "acme", Repo: "web", Number: 2}
	steps := []func() error{
		func() error {
			return s.UpsertPR(ctx, dinv.PR{Ref: ref, State: "OPEN", Reasons: []string{"review-requested"}, LastSeenAt: runAt})
		},
		func() error {
			return s.UpsertPR(ctx, dinv.PR{Ref: ref, State: "OPEN", Reasons: []string{"review-requested"}, LastSeenAt: runAt.Add(time.Minute)})
		},
		func() error {
			return s.UpsertPR(ctx, dinv.PR{Ref: ref, State: "MERGED", MergedAt: runAt.Add(2 * time.Minute), LastSeenAt: runAt.Add(3 * time.Minute)})
		},
		func() error { return s.UpsertPR(ctx, dinv.PR{Ref: other, State: "OPEN"}) },
		func() error { return s.SetOverride(ctx, dinv.Override{PR: other, Tracked: true}) },
		func() error { return s.SetOverride(ctx, dinv.Override{PR: other}) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"acme/api#1 followed review-requested 12:00:00",
		"acme/api#1 unfollowed review-requested 12:02:00",
		"acme/web#2 followed tracked 13:00:00",
		"acme/web#2 unfollowed tracked 13:00:00",
	}
	if got := scopeEvents(t, s, ref, other); !slices.Equal(got, want) {
		t.Fatalf("events:\n%q", got)
	}
}

func TestUnfollowedIsNeverBeforeItsFollowed(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.UpsertPR(ctx, dinv.PR{Ref: ref, State: "OPEN", Reasons: []string{"author"}, LastSeenAt: runAt}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPR(ctx, dinv.PR{Ref: ref, State: "MERGED", MergedAt: runAt.Add(-time.Hour), LastSeenAt: runAt.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	want := []string{"acme/api#1 followed author 12:00:00", "acme/api#1 unfollowed author 12:00:00"}
	if got := scopeEvents(t, s, ref); !slices.Equal(got, want) {
		t.Fatalf("events:\n%q", got)
	}
}
