package store

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func TestTheTrackedReasonFollowsTheOverride(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	other := pr.Ref{Owner: "acme", Repo: "web", Number: 3}
	reasons := func(r pr.Ref) []string {
		p, _, _ := db.PR(ctx, r)
		return p.Reasons
	}
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", Reasons: []string{"repo"}})
	if _, err := db.SetTracked(ctx, ref, true); err != nil {
		t.Fatal(err)
	}
	if got := reasons(ref); !slices.Equal(got, []string{"repo", pr.ReasonTracked}) {
		t.Fatalf("after track: %v", got)
	}
	if err := db.SetReasons(ctx, ref, []string{"author"}); err != nil {
		t.Fatal(err)
	}
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN"})
	if got := reasons(ref); !slices.Equal(got, []string{"author", pr.ReasonTracked}) {
		t.Fatalf("a recompute from the scope and a sweep's read dropped the track: %v", got)
	}
	if _, err := db.SetTracked(ctx, other, true); err != nil {
		t.Fatal(err)
	}
	upsert(t, db, pr.PR{Ref: other, State: "OPEN"})
	if got := reasons(other); !slices.Equal(got, []string{pr.ReasonTracked}) {
		t.Fatalf("tracked before it was stored: %v", got)
	}
	if got, _ := db.Tracked(ctx); !slices.Equal(got, []pr.Ref{ref, other}) {
		t.Fatalf("tracked: %v", got)
	}
	if changed, err := db.SetTracked(ctx, ref, false); err != nil || !changed {
		t.Fatalf("untrack: %v %v", changed, err)
	}
	if changed, err := db.SetTracked(ctx, ref, false); err != nil || changed {
		t.Fatalf("untrack again changed: %v %v", changed, err)
	}
	if got := reasons(ref); !slices.Equal(got, []string{"author"}) {
		t.Fatalf("untrack keeps the other reasons only: %v", got)
	}
}

func TestAdoptWritesItsColumnAlone(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	if _, err := db.SetTracked(ctx, ref, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAdopted(ctx, ref); err != nil {
		t.Fatal(err)
	}
	o, err := db.Override(ctx, ref)
	if err != nil || !o.Adopted || !o.Tracked {
		t.Fatalf("%+v %v", o, err)
	}
	if o, err := db.Override(ctx, pr.Ref{Owner: "acme", Repo: "api", Number: 7}); err != nil || o.Adopted || o.Tracked {
		t.Fatalf("an absent override %+v %v", o, err)
	}
}
