package collect

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

var keepNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

// newFollowStore is a fresh store holding acme/api#7,
// open, and acme/api#8, merged.
func newFollowStore(t *testing.T) *store.DB {
	t.Helper()
	st, _ := openStore(t, func() time.Time { return keepNow })
	ctx := context.Background()
	for _, p := range []pr.PR{
		{Ref: api.Ref{Owner: "acme", Repo: "api", Number: 7}, Title: "Batch export", Author: "ana", State: api.StateOpen, Reasons: []string{"review-requested"}},
		{Ref: api.Ref{Owner: "acme", Repo: "api", Number: 8}, Title: "Done", Author: "me", State: api.StateMerged, Reasons: []string{"author"}},
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
	r7 := api.Ref{Owner: "acme", Repo: "api", Number: 7}
	r8 := api.Ref{Owner: "acme", Repo: "api", Number: 8}
	for _, ref := range []api.Ref{r7, r8} {
		if err := setTracked(ctx, st, ref); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetAdopted(ctx, r8); err != nil {
		t.Fatal(err)
	}
	UntrackFinished(ctx, st, func() time.Time { return keepNow }, t.Logf)
	if got, _ := st.Tracked(ctx); !slices.Equal(got, []api.Ref{r7}) {
		t.Fatalf("tracked %v", got)
	}
	if o, _ := st.Override(ctx, r8); !o.Adopted {
		t.Fatalf("untracking dropped the adoption: %+v", o)
	}
	evs, _ := st.HostEvents(ctx, []pr.Ref{r8}, time.Time{})
	if len(evs) == 0 || evs[len(evs)-1].Kind != pr.KindUntracked || evs[len(evs)-1].Data != "merged" {
		t.Fatalf("events %+v", evs)
	}
}

func TestUntrackFinishedBudget(t *testing.T) {
	t.Parallel()
	st := newFollowStore(t)
	ctx := context.Background()
	var big []api.Ref
	for n := 1; n <= 7; n++ {
		ref := api.Ref{Owner: "acme", Repo: "big", Number: n}
		if err := st.UpsertPR(ctx, pr.PR{Ref: ref, State: api.StateOpen, Reasons: []string{"author"}}); err != nil {
			t.Fatal(err)
		}
		if err := setTracked(ctx, st, ref); err != nil {
			t.Fatal(err)
		}
		big = append(big, ref)
	}
	UntrackFinished(ctx, st, func() time.Time { return keepNow }, t.Logf)
	if got, _ := st.Tracked(ctx); !slices.Equal(got, big) {
		t.Fatalf("tracked %v, want the open ones %v", got, big)
	}
}
