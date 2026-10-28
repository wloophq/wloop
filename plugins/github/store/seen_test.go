package store

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func TestSeeRefsTouchesNothing(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	refs := make([]pr.Ref, 70)
	for i := range refs {
		refs[i] = pr.Ref{Owner: "acme", Repo: "api", Number: i + 1}
		upsert(t, db, openPR(refs[i], "author"))
	}
	writes := rowWrites(t, db, "pull_requests")
	log := listen(db)
	later := noteAt.Add(time.Hour)
	if got := log.during(t, func() error { return db.SeeRefs(ctx, refs, later) }); len(got) != 0 {
		t.Fatalf("a sighting heard %+v", got)
	}
	if w := writes(); w != 0 {
		t.Fatalf("%d pull_requests updates, want 0", w)
	}
	seen, err := db.Seen(ctx)
	if err != nil || len(seen) != len(refs) {
		t.Fatalf("seen %d, %v", len(seen), err)
	}
	for r, at := range seen {
		if !at.Equal(later) {
			t.Fatalf("%v seen %v, want %v", r, at, later)
		}
	}
}

func TestSlowUnseenSameAsBefore(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	seen, unseen, closed, absent := pr.Ref{Owner: "acme", Repo: "api", Number: 1}, pr.Ref{Owner: "acme", Repo: "api", Number: 2},
		pr.Ref{Owner: "acme", Repo: "api", Number: 3}, pr.Ref{Owner: "acme", Repo: "api", Number: 4}
	upsert(t, db, openPR(seen, "author"))
	upsert(t, db, pr.PR{Ref: unseen, State: pr.StateOpen})
	upsert(t, db, pr.PR{Ref: closed, State: "CLOSED", LastSeenAt: noteAt})
	at := noteAt.Add(time.Hour)
	if err := db.SeeRefs(ctx, []pr.Ref{seen, closed, absent}, at); err != nil {
		t.Fatal(err)
	}
	got, err := db.Seen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[seen].Equal(at) || !got[unseen].IsZero() {
		t.Fatalf("seen = %v", got)
	}
	if p, ok, err := db.PR(ctx, seen); err != nil || !ok || !p.LastSeenAt.Equal(at) {
		t.Fatalf("PR seen %v %v %v", p.LastSeenAt, ok, err)
	}
}
