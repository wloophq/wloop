package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

func batchRef(n int) github.Ref { return github.Ref{Owner: "acme", Repo: "api", Number: n} }

// seedBatch stores pull requests 1 to 20 open, one with a merged event
// lacking the merger, one in conflict, one linked to a workspace, and
// tracks 3 and 25.
func seedBatch(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for n := 1; n <= 20; n++ {
		pr := openPR(batchRef(n), "author")
		pr.Title = fmt.Sprintf("pr %d", n)
		pr.CreatedAt = noteAt.Add(-48 * time.Hour)
		pr.UpdatedAt = noteAt.Add(-time.Hour)
		if n == 7 {
			pr.Mergeable = "CONFLICTING"
		}
		upsert(t, s, pr)
		if _, err := s.AppendEvents(ctx, []dinv.Event{{PR: pr.Ref, Kind: dinv.KindOpened, Actor: "ana", At: pr.CreatedAt}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendEvents(ctx, []dinv.Event{{PR: batchRef(5), Kind: dinv.KindMerged, At: noteAt.Add(-30 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkspace(ctx, batchRef(9), dinv.Workspace{ID: "w9"}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{3, 25} {
		if err := s.SetOverride(ctx, dinv.Override{PR: batchRef(n), Tracked: true}); err != nil {
			t.Fatal(err)
		}
	}
}

// batchSnapshots are 31 snapshots over the seeded store: sightings,
// changes, a merge that fills the stored merged event, one that leaves
// scope, new pull requests (one tracked) and a ref given twice.
func batchSnapshots() []Snapshot {
	var out []Snapshot
	for n := 1; n <= 30; n++ {
		pr := openPR(batchRef(n), "author")
		pr.Title = fmt.Sprintf("pr %d", n)
		pr.CreatedAt = noteAt.Add(-48 * time.Hour)
		pr.UpdatedAt = noteAt.Add(-time.Hour)
		var evs, merges []dinv.Event
		switch {
		case n == 5:
			pr.State, pr.MergedAt, pr.Reasons = "MERGED", noteAt.Add(-30*time.Minute), nil
			merged := dinv.Event{PR: pr.Ref, Kind: dinv.KindMerged, Actor: "bob", At: pr.MergedAt, Data: "m5"}
			evs, merges = []dinv.Event{merged}, []dinv.Event{merged}
		case n == 7:
			pr.Mergeable = "UNKNOWN"
		case n == 9:
			pr.Reasons = nil
		case n == 16:
			pr.State, pr.ClosedAt = "CLOSED", noteAt.Add(-2*time.Hour)
		case n >= 11 && n <= 15:
			pr.Title = fmt.Sprintf("renamed %d", n)
			pr.UpdatedAt = noteAt
			evs = []dinv.Event{{PR: pr.Ref, Kind: dinv.KindComment, Key: fmt.Sprintf("c%d", n), Actor: "bob", At: noteAt}}
		case n > 20:
			pr.Reasons = nil
			if n%2 == 0 {
				pr.Reasons = []string{"reviewer"}
			}
			pr.Mergeable = "CONFLICTING"
			evs = []dinv.Event{
				{PR: pr.Ref, Kind: dinv.KindOpened, Actor: "ana", At: pr.CreatedAt},
				{PR: pr.Ref, Kind: dinv.KindPush, Key: fmt.Sprintf("h%d", n), Actor: "ana", At: pr.CreatedAt.Add(time.Minute)},
			}
		}
		out = append(out, Snapshot{PR: pr, Events: evs, Merges: merges})
	}
	again := out[11]
	again.PR.Title = "renamed twice"
	again.Events = append(slices.Clone(again.Events), dinv.Event{PR: again.PR.Ref, Kind: dinv.KindComment, Key: "c12b", Actor: "bob", At: noteAt})
	return append(out, again)
}

// upsertOne is UpsertPR as it was before SaveSnapshots: one transaction
// that reads the stored row and the scope, upserts, and records the flip.
func upsertOne(ctx context.Context, s *Store, pr dinv.PR) error {
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		var stored, storedMergeable, storedMergeableAt string
		err := tx.QueryRowContext(ctx, `SELECT reasons, mergeable, mergeable_at FROM pull_requests WHERE owner=? AND repo=? AND number=?`, pr.Ref.Owner, pr.Ref.Repo, pr.Ref.Number).
			Scan(&stored, &storedMergeable, &storedMergeableAt)
		isNew := errors.Is(err, sql.ErrNoRows)
		if err != nil && !isNew {
			return err
		}
		mergeable, mergeableAt := nextMergeable(storedMergeable, storedMergeableAt, pr.Mergeable, pr.LastSeenAt)
		storedReasons, err := splitList(stored)
		if err != nil {
			return err
		}
		tracked, err := isTracked(ctx, tx, pr.Ref)
		if err != nil {
			return err
		}
		reasons := withTracked(append(slices.Clone(pr.Reasons), storedReasons...), tracked)
		before, err := scopes(ctx, tx, pr.Ref)
		if err != nil {
			return err
		}
		failing := ""
		if len(pr.FailingChecks) > 0 {
			raw, err := json.Marshal(pr.FailingChecks)
			if err != nil {
				return err
			}
			failing = string(raw)
		}
		res, err := tx.ExecContext(ctx, upsertPRSQL, pr.Ref.Owner, pr.Ref.Repo, pr.Ref.Number, pr.Title, pr.Author, pr.State, pr.IsDraft, pr.HeadOid, pr.HeadBranch, pr.BaseBranch,
			pr.IsFork, pr.ChecksState, pr.ReviewDecision, joinList(pr.ReviewRequests), pr.URL, ts(pr.CreatedAt), ts(pr.UpdatedAt), ts(pr.MergedAt), ts(pr.ClosedAt), joinList(reasons),
			ts(pr.BodyEditedAt), pr.ChangedFiles, failing, mergeable, mergeableAt)
		if err != nil {
			return err
		}
		if changed, err := res.RowsAffected(); err != nil || changed == 0 {
			if err != nil {
				return err
			}
			return seeRefs(ctx, tx, []github.Ref{pr.Ref}, pr.LastSeenAt)
		}
		if err := seeRefs(ctx, tx, []github.Ref{pr.Ref}, pr.LastSeenAt); err != nil {
			return err
		}
		topics := port.TopicPRs
		if isNew || joinList(reasons) != stored {
			topics |= port.TopicFollow
		}
		tx.Touch(topics, pr.Ref.Unit())
		return s.recordScope(ctx, tx, before, pr.LastSeenAt, pr.Ref)
	})
	return err
}

type storedState struct {
	PRs       []dinv.PR
	Events    map[github.Ref][]dinv.Event
	Overrides map[github.Ref]dinv.Override
}

func stateOf(t *testing.T, s *Store) storedState {
	t.Helper()
	ctx := context.Background()
	prs, err := s.ListPRs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var refs []github.Ref
	for n := 1; n <= 30; n++ {
		refs = append(refs, batchRef(n))
	}
	evs, err := s.EventsByPR(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	ovs, err := s.Overrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return storedState{prs, evs, ovs}
}

func TestSaveSnapshotsSameAsUpsertLoop(t *testing.T) {
	seenAt := noteAt.Add(time.Hour)
	ctx := context.Background()

	batch, batchLog := openNoted(t)
	batch.UseClock(func() time.Time { return noteAt })
	seedBatch(t, batch)
	from := batchLog.Gen()
	var added map[github.Ref]int
	got, notes := noted(t, batchLog, func() (err error) {
		added, err = batch.SaveSnapshots(ctx, batchSnapshots(), seenAt)
		return err
	})
	if notes != 1 {
		t.Fatalf("SaveSnapshots noted %d times, want once", notes)
	}

	loop, loopLog := openNoted(t)
	loop.UseClock(func() time.Time { return noteAt })
	seedBatch(t, loop)
	loopFrom := loopLog.Gen()
	loopAdded := map[github.Ref]int{}
	for _, snap := range batchSnapshots() {
		pr := snap.PR
		pr.LastSeenAt = seenAt
		if err := upsertOne(ctx, loop, pr); err != nil {
			t.Fatal(err)
		}
		n, err := loop.AppendEvents(ctx, snap.Events)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			loopAdded[pr.Ref] += n
		}
		for _, m := range snap.Merges {
			if _, err := loop.FillMerged(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
	}

	if b, l := stateOf(t, batch), stateOf(t, loop); !reflect.DeepEqual(b, l) {
		t.Fatalf("stored state differs\nbatch: %+v\nloop:  %+v", b, l)
	}
	if !reflect.DeepEqual(added, loopAdded) {
		t.Fatalf("added %v, loop added %v", added, loopAdded)
	}
	all := port.Topic(1<<32 - 1)
	if want := loopLog.Since(loopFrom, all); got.Topics != want.Topics || !slices.Equal(got.Units, want.Units) {
		t.Fatalf("batch noted %+v, the loop %+v", got, want)
	}
	if got := batchLog.Since(from, all); got.Topics&port.TopicLoopEvents == 0 {
		t.Fatalf("no followed or unfollowed event noted: %+v", got)
	}
	events := stateOf(t, batch).Events
	for n, want := range map[int]string{5: dinv.KindUnfollowed, 16: dinv.KindUnfollowed, 22: dinv.KindFollowed, 25: dinv.KindFollowed, 21: ""} {
		var flips []string
		for _, e := range events[batchRef(n)] {
			if e.Kind == dinv.KindFollowed || e.Kind == dinv.KindUnfollowed {
				flips = append(flips, e.Kind)
			}
		}
		if (want == "" && len(flips) > 0) || (want != "" && !slices.Contains(flips, want)) {
			t.Errorf("#%d scope events %v, want %q", n, flips, want)
		}
	}
}

func TestSaveSnapshotsKeepsNewestFollowedForADuplicateRef(t *testing.T) {
	seenAt := noteAt.Add(time.Hour)
	later := seenAt.Add(2 * time.Hour)
	ctx := context.Background()
	closed := openPR(ref, "author")
	closed.State, closed.ClosedAt = "CLOSED", seenAt.Add(-time.Hour)
	snaps := []Snapshot{{PR: closed}, {PR: openPR(ref, "author")}, {PR: closed}}
	seed := func(t *testing.T) *Store {
		s, _ := openNoted(t)
		pr := openPR(ref, "author")
		pr.LastSeenAt = later
		upsert(t, s, pr)
		return s
	}

	batch := seed(t)
	if _, err := batch.SaveSnapshots(ctx, snaps, seenAt); err != nil {
		t.Fatal(err)
	}
	loop := seed(t)
	for _, snap := range snaps {
		pr := snap.PR
		pr.LastSeenAt = seenAt
		if err := upsertOne(ctx, loop, pr); err != nil {
			t.Fatal(err)
		}
	}
	b, l := stateOf(t, batch).Events[ref], stateOf(t, loop).Events[ref]
	if !reflect.DeepEqual(b, l) {
		t.Fatalf("events differ\nbatch: %+v\nloop:  %+v", b, l)
	}
	for _, e := range b {
		if e.Kind == dinv.KindUnfollowed && e.At.Before(later) {
			t.Fatalf("unfollowed at %v, before the newest followed %v", e.At, later)
		}
	}
}
