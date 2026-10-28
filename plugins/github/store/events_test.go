package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func scopeEvents(t *testing.T, db *DB, refs ...pr.Ref) []string {
	t.Helper()
	evs, err := db.HostEvents(context.Background(), refs, time.Time{}, pr.KindFollowed, pr.KindUnfollowed)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range refs {
		for _, e := range evs {
			if e.PR == r {
				out = append(out, r.String()+" "+e.Kind+" "+e.Data+" "+e.At.Format(time.TimeOnly))
			}
		}
	}
	return out
}

func keptKinds(h *memHost, ref pr.Ref) map[string]int {
	out := map[string]int{}
	for _, k := range h.kinds(ref) {
		out[k]++
	}
	return out
}

func TestFollowedEventsOnScopeChange(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	now := noteAt
	db.UseClock(func() time.Time { now = now.Add(time.Minute); return now })
	other := pr.Ref{Owner: "acme", Repo: "web", Number: 2}
	for _, r := range []pr.Ref{ref, other} {
		upsert(t, db, pr.PR{Ref: r, State: pr.StateOpen})
	}
	steps := []func() error{
		func() error { return db.SetReasons(ctx, ref, []string{"author", "repo"}) },
		func() error { return db.SetReasons(ctx, ref, []string{"author"}) },
		func() error { return db.SetReasons(ctx, ref, nil) },
		func() error { return db.SetWorkspace(ctx, ref, pr.Workspace{ID: "ws1"}) },
		func() error { return db.ReplaceWorkspaces(ctx, map[pr.Ref]pr.Workspace{other: {ID: "ws2"}}) },
		func() error { return db.SetWorkspace(ctx, other, pr.Workspace{ID: "ws3"}) },
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
	if got := scopeEvents(t, db, ref, other); !slices.Equal(got, want) {
		t.Fatalf("events:\n%q", got)
	}
}

func TestFollowedEventsOnUpsertAndTrack(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	db.UseClock(func() time.Time { return noteAt.Add(time.Hour) })
	other := pr.Ref{Owner: "acme", Repo: "web", Number: 2}
	steps := []func() error{
		func() error {
			return db.UpsertPR(ctx, pr.PR{Ref: ref, State: pr.StateOpen, Reasons: []string{"review-requested"}, LastSeenAt: noteAt})
		},
		func() error {
			return db.UpsertPR(ctx, pr.PR{Ref: ref, State: pr.StateOpen, Reasons: []string{"review-requested"}, LastSeenAt: noteAt.Add(time.Minute)})
		},
		func() error {
			return db.UpsertPR(ctx, pr.PR{Ref: ref, State: "MERGED", MergedAt: noteAt.Add(2 * time.Minute), LastSeenAt: noteAt.Add(3 * time.Minute)})
		},
		func() error { return db.UpsertPR(ctx, pr.PR{Ref: other, State: pr.StateOpen}) },
		func() error { _, err := db.SetTracked(ctx, other, true); return err },
		func() error { _, err := db.SetTracked(ctx, other, false); return err },
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
	if got := scopeEvents(t, db, ref, other); !slices.Equal(got, want) {
		t.Fatalf("events:\n%q", got)
	}
}

func TestUnfollowedIsNeverBeforeItsFollowed(t *testing.T) {
	t.Parallel()
	db := open(t)
	upsert(t, db, pr.PR{Ref: ref, State: pr.StateOpen, Reasons: []string{"author"}, LastSeenAt: noteAt})
	upsert(t, db, pr.PR{Ref: ref, State: "MERGED", MergedAt: noteAt.Add(-time.Hour), LastSeenAt: noteAt.Add(time.Minute)})
	want := []string{"acme/api#1 followed author 12:00:00", "acme/api#1 unfollowed author 12:00:00"}
	if got := scopeEvents(t, db, ref); !slices.Equal(got, want) {
		t.Fatalf("events:\n%q", got)
	}
}

func TestSaveSnapshotsRecordsItsEventsOnTheHostBeforeItsRows(t *testing.T) {
	t.Parallel()
	db := open(t)
	h := hostOf(db)
	ctx := context.Background()
	p := pr.PR{Ref: ref, Title: "one", Author: "ana", State: pr.StateOpen, Reasons: []string{"author"}}
	push := pr.Event{PR: ref, Kind: pr.KindPush, Key: "abc", Actor: "ana", At: noteAt}
	merged := pr.Event{PR: ref, Kind: pr.KindMerged, At: noteAt.Add(time.Hour)}
	added, err := db.SaveSnapshots(ctx, []Snapshot{{PR: p, Events: []pr.Event{push, merged}}}, noteAt)
	if err != nil {
		t.Fatal(err)
	}
	if added[ref] != 2 {
		t.Errorf("added %v, want 2 on %s", added, ref)
	}
	if got := keptKinds(h, ref); got[pr.KindPush] != 1 || got[pr.KindMerged] != 1 || got[pr.KindFollowed] != 1 || len(got) != 3 {
		t.Errorf("the host got %v", got)
	}

	filled := merged
	filled.Actor, filled.Data = "bruno", "def"
	before := len(h.kinds(ref))
	added, err = db.SaveSnapshots(ctx, []Snapshot{{PR: p, Events: []pr.Event{push, merged}, Merges: []pr.Event{filled}}}, noteAt.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(h.kinds(ref)) != before {
		t.Errorf("a second save added %v and recorded %d events, want none new", added, len(h.kinds(ref))-before)
	}
	facts, err := db.StoredFacts(ctx, []pr.Ref{ref})
	if err != nil || facts[ref].Pushes["abc"] != noteAt || len(facts[ref].UnfilledMerges) != 1 || !facts[ref].Found {
		t.Fatalf("stored facts %+v: %v", facts[ref], err)
	}
}

func TestSaveSnapshotsStoresNothingWhenTheHostRefusesItsEvents(t *testing.T) {
	t.Parallel()
	db := open(t)
	h := hostOf(db)
	ctx := context.Background()
	p := pr.PR{Ref: ref, Title: "one", Author: "ana", State: pr.StateOpen, Reasons: []string{"author"}}
	push := pr.Event{PR: ref, Kind: pr.KindPush, Key: "abc", Actor: "ana", At: noteAt}
	h.fail = errors.New("broker down")
	if _, err := db.SaveSnapshots(ctx, []Snapshot{{PR: p, Events: []pr.Event{push}}}, noteAt); err == nil {
		t.Fatal("want the host's error")
	}
	if _, ok, err := db.PR(ctx, ref); err != nil || ok {
		t.Fatalf("a save whose events the host refused stored the pull request (%v)", err)
	}
	if _, err := db.SaveSnapshots(ctx, []Snapshot{{PR: p, Events: []pr.Event{push}}}, noteAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := keptKinds(h, ref); got[pr.KindPush] != 1 || got[pr.KindFollowed] != 1 {
		t.Errorf("after the retry the host holds %v, want one push and one followed", got)
	}
}

// failPRWrites makes every write of a pull request row fail until the
// returned func runs.
func failPRWrites(t *testing.T, db *DB) func() {
	t.Helper()
	exec(t, db, `CREATE TRIGGER fail_pr_insert BEFORE INSERT ON pull_requests BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	exec(t, db, `CREATE TRIGGER fail_pr_update BEFORE UPDATE ON pull_requests BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	return func() { exec(t, db, `DROP TRIGGER fail_pr_insert; DROP TRIGGER fail_pr_update`) }
}

func TestAWriteThatFailsAfterTheHostLeavesEachEventOnce(t *testing.T) {
	t.Parallel()
	db := open(t)
	h := hostOf(db)
	ctx := context.Background()
	p := pr.PR{Ref: ref, Title: "one", Author: "ana", State: pr.StateOpen, Reasons: []string{"author"}, UpdatedAt: noteAt}
	push := pr.Event{PR: ref, Kind: pr.KindPush, Key: "abc", Actor: "ana", At: noteAt}
	heal := failPRWrites(t, db)
	if _, err := db.SaveSnapshots(ctx, []Snapshot{{PR: p, Events: []pr.Event{push}}}, noteAt); err == nil {
		t.Fatal("want the write to fail")
	}
	if _, ok, err := db.PR(ctx, ref); err != nil || ok {
		t.Fatalf("the failed write stored the pull request (%v), so the next ingest would skip it", err)
	}
	heal()
	added, err := db.SaveSnapshots(ctx, []Snapshot{{PR: p, Events: []pr.Event{push}}}, noteAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("the retry added %v, want none: the host took them before the write failed", added)
	}
	if got := keptKinds(h, ref); got[pr.KindPush] != 1 || got[pr.KindFollowed] != 1 {
		t.Errorf("after the retry the host holds %v, want one push and one followed", got)
	}
}

func TestSetReasonsRecordsItsFlipOnceAcrossAFailedWrite(t *testing.T) {
	t.Parallel()
	db := open(t)
	h := hostOf(db)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, Title: "one", State: pr.StateOpen})
	heal := failPRWrites(t, db)
	if err := db.SetReasons(ctx, ref, []string{"author"}); err == nil {
		t.Fatal("want the write to fail")
	}
	heal()
	if err := db.SetReasons(ctx, ref, []string{"author"}); err != nil {
		t.Fatal(err)
	}
	if got := keptKinds(h, ref); got[pr.KindFollowed] != 1 {
		t.Errorf("the host holds %v, want one followed", got)
	}
	if err := db.SetReasons(ctx, ref, nil); err != nil {
		t.Fatal(err)
	}
	if got := keptKinds(h, ref); got[pr.KindFollowed] != 1 || got[pr.KindUnfollowed] != 1 {
		t.Errorf("after unfollowing the host holds %v, want one followed and one unfollowed", got)
	}
}

func TestAStoreWithNoHostRefusesToDeriveEvents(t *testing.T) {
	t.Parallel()
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.UpsertPR(context.Background(), openPR(ref, "author")); err == nil {
		t.Fatal("a store with no host saved a pull request whose scope flips")
	}
}

func TestTouchStubsRefreshesTheLightFacts(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, State: pr.StateOpen, ReviewRequests: []string{"ana"}, Mergeable: "MERGEABLE", LastSeenAt: noteAt})
	later := noteAt.Add(time.Hour)
	stub := Stub{Ref: ref, State: pr.StateOpen, Mergeable: "CONFLICTING", ReviewRequests: []string{"bruno"}, ReviewDecision: "APPROVED"}
	missing := Stub{Ref: pr.Ref{Owner: "acme", Repo: "api", Number: 9}}
	if err := db.TouchStubs(ctx, []Stub{stub, missing}, later); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.PR(ctx, ref)
	if err != nil || !slices.Equal(got.ReviewRequests, []string{"bruno"}) || got.ReviewDecision != "APPROVED" || got.Mergeable != "CONFLICTING" ||
		!got.MergeableAt.Equal(later) || !got.LastSeenAt.Equal(later) {
		t.Fatalf("after the touch %+v: %v", got, err)
	}
	if _, ok, _ := db.PR(ctx, missing.Ref); ok {
		t.Fatal("a stub of a pull request the store lacks stored one")
	}
}

var _ Host = (*memHost)(nil)
