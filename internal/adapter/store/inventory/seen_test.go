package inventory

import (
	"context"
	"testing"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestSeeRefsTouchesNothing(t *testing.T) {
	s, log := openNoted(t)
	refs := make([]github.Ref, 70)
	for i := range refs {
		refs[i] = github.Ref{Owner: "acme", Repo: "api", Number: i + 1}
		upsert(t, s, openPR(refs[i], "author"))
	}
	writes := countRowWrites(t, s)
	ctx, sc := metertest.Start(t, context.Background())
	later := noteAt.Add(time.Hour)
	if got, n := noted(t, log, func() error { return s.SeeRefs(ctx, refs, later) }); n != 0 {
		t.Fatalf("%d notes: %+v", n, got)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL, Op: "exec"}: 1})
	if w := writes(); w != 0 {
		t.Fatalf("%d pull_requests updates, want 0", w)
	}
	seen, err := s.Seen(ctx)
	if err != nil || len(seen) != len(refs) {
		t.Fatalf("seen %d, %v", len(seen), err)
	}
	for ref, at := range seen {
		if !at.Equal(later) {
			t.Fatalf("%v seen %v, want %v", ref, at, later)
		}
	}
}

func TestSlowUnseenSameAsBefore(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	seen, unseen, closed, absent := github.Ref{Owner: "acme", Repo: "api", Number: 1}, github.Ref{Owner: "acme", Repo: "api", Number: 2},
		github.Ref{Owner: "acme", Repo: "api", Number: 3}, github.Ref{Owner: "acme", Repo: "api", Number: 4}
	upsert(t, s, openPR(seen, "author"))
	upsert(t, s, dinv.PR{Ref: unseen, State: dgh.StateOpen})
	upsert(t, s, dinv.PR{Ref: closed, State: "CLOSED", LastSeenAt: noteAt})
	at := noteAt.Add(time.Hour)
	if err := s.SeeRefs(ctx, []github.Ref{seen, closed, absent}, at); err != nil {
		t.Fatal(err)
	}
	got, err := s.Seen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[seen].Equal(at) || !got[unseen].IsZero() {
		t.Fatalf("seen = %v", got)
	}
	if pr, ok, err := s.PR(ctx, seen); err != nil || !ok || !pr.LastSeenAt.Equal(at) {
		t.Fatalf("PR seen %v %v %v", pr.LastSeenAt, ok, err)
	}
}
