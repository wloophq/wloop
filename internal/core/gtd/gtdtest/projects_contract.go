package gtdtest

import (
	"context"
	"slices"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

// ProjectsContract is the suite every ProjectStore passes; open gives an empty entry store and the project
// store over it.
func ProjectsContract(t *testing.T, open func(t *testing.T) (coregtd.Store, coregtd.ProjectStore)) {
	ctx := context.Background()
	project := func(id gtd.ID, slug string, st gtd.State) gtd.Entry {
		return gtd.Entry{ID: id, Kind: gtd.KindProject, State: st, Title: "P " + string(id), Origin: gtd.OriginUser, Sequential: true,
			ReviewEvery: 14 * 24 * time.Hour, ReviewedAt: t0.Add(-time.Hour), Slug: slug, Created: t0, Updated: t0}
	}

	t.Run("project fields round-trip", func(t *testing.T) {
		s, _ := open(t)
		p := project("e-p1", "ship-it", gtd.Next)
		apply(t, s, p)
		got, err := s.Entry(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Same(p) {
			t.Fatalf("read %+v, want %+v", got, p)
		}
		before := position(t, s)
		apply(t, s, p)
		if after := position(t, s); after != before {
			t.Errorf("an Apply of the same project moved the position from %v to %v", before, after)
		}
		p.ReviewedAt, p.Updated = t0, t0.Add(time.Minute)
		apply(t, s, p)
		if got, _ := s.Entry(ctx, p.ID); !got.ReviewedAt.Equal(t0) {
			t.Errorf("reviewed at %v, want %v", got.ReviewedAt, t0)
		}
		ps, err := s.Entries(ctx, gtd.Query{View: gtd.ViewProjects})
		if err != nil || len(ps) != 1 || ps[0].Slug != "ship-it" || ps[0].ReviewEvery != 14*24*time.Hour {
			t.Errorf("projects view %+v, %v", ps, err)
		}
	})

	t.Run("slugs stay taken", func(t *testing.T) {
		s, ps := open(t)
		apply(t, s, project("e-p1", "ship-it", gtd.Next), project("e-p2", "old", gtd.Done))
		for slug, want := range map[string]bool{"ship-it": true, "old": true, "other": false} {
			if got, err := ps.SlugTaken(ctx, slug); err != nil || got != want {
				t.Errorf("SlugTaken(%q) = %v, %v; want %v", slug, got, err, want)
			}
		}
	})

	t.Run("review runs", func(t *testing.T) {
		_, ps := open(t)
		if _, ok, err := ps.CurrentRun(ctx); ok || err != nil {
			t.Fatalf("a fresh store has a current run: %v, %v", ok, err)
		}
		r := gtd.ReviewRun{ID: "r1", StartedAt: t0, Done: []string{"outbox"}}
		if err := ps.PutRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		r.Done = append(r.Done, "inbox")
		if err := ps.PutRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		got, ok, err := ps.CurrentRun(ctx)
		if err != nil || !ok || got.ID != "r1" || !got.StartedAt.Equal(t0) || !slices.Equal(got.Done, []string{"outbox", "inbox"}) {
			t.Fatalf("current %+v, %v, %v", got, ok, err)
		}
		r.FinishedAt = t0.Add(time.Hour)
		if err := ps.PutRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := ps.CurrentRun(ctx); ok {
			t.Error("a finished run is still current")
		}
		if err := ps.PutRun(ctx, gtd.ReviewRun{ID: "r2", StartedAt: t0.Add(2 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if got, ok, _ := ps.CurrentRun(ctx); !ok || got.ID != "r2" || len(got.Done) != 0 {
			t.Errorf("current after a new start %+v, %v", got, ok)
		}
	})
}
