package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func detail(readAt time.Time, labels ...string) Detail {
	return Detail{Version: DetailVersion, ReadAt: readAt, Labels: labels, ThreadsTotal: 1}
}

func TestUnchangedDetailNotRewritten(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, openPR(ref, "author"))
	if err := db.SaveDetail(ctx, ref, detail(noteAt, "bug")); err != nil {
		t.Fatal(err)
	}
	writes := rowWrites(t, db, "pr_details")
	log := listen(db)
	if got := log.during(t, func() error { return db.SaveDetail(ctx, ref, detail(noteAt, "bug")) }); len(got) != 0 {
		t.Fatalf("re-storing the same read heard %+v", got)
	}
	if n := writes(); n != 0 {
		t.Fatalf("%d writes to pr_details, want 0", n)
	}

	later := noteAt.Add(time.Hour)
	if got := log.during(t, func() error { return db.SaveDetail(ctx, ref, detail(later, "bug")) }); len(got) != 0 {
		t.Fatalf("a later read of the same detail heard %+v", got)
	}
	if n := writes(); n != 1 {
		t.Fatalf("%d writes to pr_details for a new read time, want 1", n)
	}
	d, err := db.Details(ctx, []pr.Ref{ref})
	if err != nil || !d[ref].ReadAt.Equal(later) || d[ref].Version != 2 {
		t.Fatalf("stored %+v: %v", d[ref], err)
	}

	got := log.during(t, func() error { return db.SaveDetail(ctx, ref, detail(later, "bug", "urgent")) })
	if len(got) != 1 || !slices.Equal(got[0].Refs, []pr.Ref{ref}) {
		t.Fatalf("a new detail heard %+v", got)
	}
	d, err = db.Details(ctx, []pr.Ref{ref, {Owner: "acme", Repo: "api", Number: 9}})
	if err != nil || len(d) != 1 || !slices.Equal(d[ref].Labels, []string{"bug", "urgent"}) {
		t.Fatalf("stored %+v: %v", d, err)
	}
}
