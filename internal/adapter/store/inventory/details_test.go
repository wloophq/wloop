package inventory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func fullDetail(readAt time.Time) *Detail {
	at := noteAt
	return &Detail{
		Version: DetailVersion, ReadAt: readAt, AuthorAssociation: "MEMBER", Labels: []string{"bug"},
		Commits:       []pr.Commit{{Oid: "c1", Parents: 1}},
		Reviews:       []pr.Review{{ID: "r1", State: pr.ReviewApproved, Author: "bob", SubmittedAt: at.Add(-time.Hour), CommitOid: "c1", HasContent: true}},
		LatestReviews: []pr.Review{{ID: "r1", State: pr.ReviewApproved, Author: "bob", SubmittedAt: at.Add(-time.Hour), CommitOid: "c1"}},
		Comments:      []pr.Comment{{ID: "k1", Author: "ana", CreatedAt: at.Add(-2 * time.Hour)}},
		Checks:        []pr.Check{{Name: "lint", Failed: true, URL: "https://ci/1"}},
		ThreadsTotal:  2, OpenThreads: []pr.Thread{{LastAuthor: "bob", LastAt: at.Add(-time.Minute)}}, ThreadsRead: true,
	}
}

// detailWrites counts the statements of sc that write pr_details.
func detailWrites(sc *meter.Scope) int {
	n := 0
	for _, x := range sc.Samples() {
		if x.Res == meter.SQL && x.Op == "exec" && strings.Contains(x.Detail, "pr_details") {
			n++
		}
	}
	return n
}

func TestUnchangedDetailNotRewritten(t *testing.T) {
	s, log := openNoted(t)
	pr := openPR(ref, "author")
	snap := []Snapshot{{PR: pr, Detail: fullDetail(noteAt)}}
	if _, err := s.SaveSnapshots(context.Background(), snap, noteAt); err != nil {
		t.Fatal(err)
	}
	ctx, sc := metertest.Start(t, context.Background())
	if got, n := noted(t, log, func() error { _, err := s.SaveSnapshots(ctx, snap, noteAt); return err }); n != 0 {
		t.Fatalf("re-storing the same read noted %d times: %+v", n, got)
	}
	if n := detailWrites(sc); n != 0 {
		t.Fatalf("%d writes to pr_details, want 0", n)
	}

	later := noteAt.Add(time.Hour)
	ctx, sc = metertest.Start(t, context.Background())
	if got, n := noted(t, log, func() error {
		_, err := s.SaveSnapshots(ctx, []Snapshot{{PR: pr, Detail: fullDetail(later)}}, later)
		return err
	}); n != 0 {
		t.Fatalf("a later read of the same detail noted %d times: %+v", n, got)
	}
	if n := detailWrites(sc); n != 1 {
		t.Fatalf("%d writes to pr_details for a new read time, want 1", n)
	}
	d, err := s.Details(context.Background(), []github.Ref{ref})
	if err != nil || !d[ref].ReadAt.Equal(later) || d[ref].Version != DetailVersion || len(d[ref].OpenThreads) != 1 || d[ref].Reviews[0].HasContent != true {
		t.Fatalf("stored %+v: %v", d[ref], err)
	}

	moved := fullDetail(later)
	moved.Labels = append(moved.Labels, "urgent")
	got, n := noted(t, log, func() error {
		_, err := s.SaveSnapshots(context.Background(), []Snapshot{{PR: pr, Detail: moved}}, later)
		return err
	})
	if n != 1 || got.Units == nil || got.Units[0] != ref.Unit() {
		t.Fatalf("a new detail noted %d times: %+v", n, got)
	}

	if _, n := noted(t, log, func() error {
		_, err := s.SaveSnapshots(context.Background(), []Snapshot{{PR: pr}}, later)
		return err
	}); n != 0 {
		t.Fatalf("a snapshot without detail noted %d times", n)
	}
	if d, _ := s.Details(context.Background(), []github.Ref{ref}); len(d[ref].Labels) != 2 {
		t.Fatalf("a snapshot without detail dropped the stored one: %+v", d[ref])
	}
}

func TestDetailsOneQuery(t *testing.T) {
	s, _ := openNoted(t)
	refs := make([]github.Ref, 69)
	snaps := make([]Snapshot, 69)
	for i := range refs {
		refs[i] = github.Ref{Owner: "acme", Repo: "api", Number: i + 1}
		d := fullDetail(noteAt)
		d.Comments[0].ID = fmt.Sprintf("k%d", i)
		snaps[i] = Snapshot{PR: openPR(refs[i], "author"), Detail: d}
	}
	if _, err := s.SaveSnapshots(context.Background(), snaps, noteAt); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func(context.Context, []github.Ref) (map[github.Ref]Detail, error){"Details": s.Details, "DetailStamps": s.DetailStamps} {
		ctx, sc := metertest.Start(t, context.Background())
		got, err := read(ctx, refs)
		if err != nil || len(got) != 69 {
			t.Fatalf("%s: %d details: %v", name, len(got), err)
		}
		metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 1, {Res: meter.Tx, Op: "write"}: 0})
		if d := got[refs[9]]; !d.ReadAt.Equal(noteAt) || d.Version != DetailVersion {
			t.Fatalf("%s: %+v", name, d)
		}
		if name == "Details" && got[refs[9]].Comments[0].ID != "k9" {
			t.Fatalf("Details: %+v", got[refs[9]])
		}
		if name == "DetailStamps" && got[refs[9]].Comments != nil {
			t.Fatalf("DetailStamps read the body: %+v", got[refs[9]])
		}
	}
}
