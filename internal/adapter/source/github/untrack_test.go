package ghsource

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

var keepNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

// newFollowStore is a fresh store holding acme/api#7,
// open, and acme/api#8, merged.
func newFollowStore(t *testing.T) *inventory.Store {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := inventory.New(db)
	st.UseClock(func() time.Time { return keepNow })
	ctx := context.Background()
	for _, p := range []dinv.PR{
		{Ref: github.Ref{Owner: "acme", Repo: "api", Number: 7}, Title: "Batch export", Author: "ana", State: dgh.StateOpen, Reasons: []string{"review-requested"}},
		{Ref: github.Ref{Owner: "acme", Repo: "api", Number: 8}, Title: "Done", Author: "me", State: dgh.StateMerged, Reasons: []string{"author"}},
	} {
		if err := st.UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestAFinishedPullRequestIsUntracked(t *testing.T) {
	t.Parallel()
	st := newFollowStore(t)
	ctx := context.Background()
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	for _, ref := range []github.Ref{r7, r8} {
		if err := st.SetOverride(ctx, dinv.Override{PR: ref, Tracked: true, Hold: true}); err != nil {
			t.Fatal(err)
		}
	}
	UntrackFinished(ctx, st, func() time.Time { return keepNow }, t.Logf)
	if got, _ := st.Tracked(ctx); !slices.Equal(got, []github.Ref{r7}) {
		t.Fatalf("tracked %v", got)
	}
	if o, _ := st.Override(ctx, r8); !o.Hold {
		t.Fatalf("untracking dropped the hold: %+v", o)
	}
	evs, _ := st.Events(ctx, r8)
	if len(evs) == 0 || evs[len(evs)-1].Kind != dinv.KindUntracked || evs[len(evs)-1].Data != "merged" {
		t.Fatalf("events %+v", evs)
	}
}

func TestUntrackFinishedBudget(t *testing.T) {
	t.Parallel()
	st := newFollowStore(t)
	ctx, sc := metertest.Start(t, context.Background())
	for n := 1; n <= 7; n++ {
		ref := github.Ref{Owner: "acme", Repo: "big", Number: n}
		if err := st.UpsertPR(ctx, dinv.PR{Ref: ref, State: dgh.StateOpen, Reasons: []string{"author"}}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetOverride(ctx, dinv.Override{PR: ref, Tracked: true}); err != nil {
			t.Fatal(err)
		}
	}
	before := metertest.Count(sc, metertest.Limit{Res: meter.SQL})
	UntrackFinished(ctx, st, func() time.Time { return keepNow }, t.Logf)
	if n := metertest.Count(sc, metertest.Limit{Res: meter.SQL}) - before; n != 2 {
		t.Fatalf("%d statements at 7 tracked, want 2\n%s", n, metertest.Report(sc.Tally()))
	}
}
