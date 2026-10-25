package sqlite_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

type outboxSource struct{ drafts []gtd.Entry }

func (o outboxSource) Entries(context.Context, gtd.Query) ([]gtd.Entry, error) { return o.drafts, nil }

func (o outboxSource) Entry(_ context.Context, id gtd.ID) (gtd.Entry, bool, error) {
	for _, d := range o.drafts {
		if d.ID == id {
			return d, true, nil
		}
	}
	return gtd.Entry{}, false, nil
}

func (o outboxSource) Count(context.Context, string) (int, error) { return len(o.drafts), nil }

var (
	countAreas    = []string{"", "OSS", "Hiring"}
	countContexts = []string{"@decide", "@reply", "@agent", "@repo-op"}
	countStates   = []gtd.State{gtd.Inbox, gtd.Next, gtd.Next, gtd.Waiting, gtd.Scheduled, gtd.Someday, gtd.Reference, gtd.Done, gtd.Trash}
)

// randomEntries spans areas, contexts and projects, some sequential and some ended.
func randomEntries(r *rand.Rand, n int) []gtd.Entry {
	var out []gtd.Entry
	var projects []gtd.ID
	for i := range n {
		at := entryAt.Add(time.Duration(r.IntN(10000)) * time.Minute)
		e := gtd.Entry{ID: gtd.ID(fmt.Sprintf("e-%016x", i)), Kind: gtd.KindCapture, State: countStates[r.IntN(len(countStates))],
			Title: fmt.Sprint(i), Origin: gtd.OriginUser, Area: countAreas[r.IntN(len(countAreas))], Created: at, Updated: at,
			Focus: gtd.Focus{On: r.IntN(4) == 0}}
		for _, c := range countContexts {
			if r.IntN(3) == 0 {
				e.Contexts = append(e.Contexts, c)
			}
		}
		switch {
		case r.IntN(8) == 0:
			e.Kind, e.Sequential = gtd.KindProject, r.IntN(2) == 0
			projects = append(projects, e.ID)
		case len(projects) > 0 && r.IntN(2) == 0:
			e.Project, e.Position = projects[r.IntN(len(projects))], r.IntN(5)
		}
		out = append(out, e)
	}
	return out
}

func TestCountersEqualTheirLists(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(15, 2))
	ctx := context.Background()
	for round := range 20 {
		entries := randomEntries(r, 20+r.IntN(80))
		drafts := make([]gtd.Entry, r.IntN(4))
		t.Run(fmt.Sprint("round", round), func(t *testing.T) {
			t.Parallel()
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "counts.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := db.Entries()
			applyEntries(t, store, entries...)
			svc := coregtd.New(coregtd.Deps{Store: store, Clock: fixedNow{entryAt}})
			svc.RegisterView(gtd.ViewOutbox, "draft:", outboxSource{drafts: drafts})
			for _, area := range countAreas {
				c, err := svc.Counts(ctx, area)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range append([]gtd.View{gtd.ViewOutbox}, gtd.StoredViews...) {
					list, err := svc.Entries(ctx, gtd.Query{View: v, Area: area})
					if err != nil {
						t.Fatal(err)
					}
					if got, ok := c.Views[v]; !ok || got != len(list) {
						t.Errorf("round %d area %q view %s: counter %d (%v), list %d", round, area, v, got, ok, len(list))
					}
				}
				if c.Badge != c.Views[gtd.ViewInbox]+c.Views[gtd.ViewOutbox] {
					t.Errorf("round %d area %q: badge %d", round, area, c.Badge)
				}
				next, err := svc.Entries(ctx, gtd.Query{View: gtd.ViewNext})
				if err != nil {
					t.Fatal(err)
				}
				for _, a := range countAreas[1:] {
					n := len(gtd.Select(next, gtd.Query{View: gtd.ViewNext, Area: a}))
					if c.Areas[a] != n {
						t.Errorf("round %d area %q: Areas[%s] %d, list %d", round, area, a, c.Areas[a], n)
					}
				}
				for _, cx := range countContexts {
					n := len(gtd.Select(next, gtd.Query{View: gtd.ViewNext, Area: area, Contexts: []string{cx}}))
					if c.Contexts[cx] != n {
						t.Errorf("round %d area %q: Contexts[%s] %d, list %d", round, area, cx, c.Contexts[cx], n)
					}
				}
			}
		})
	}
}

type fixedNow struct{ t time.Time }

func (f fixedNow) Now() time.Time { return f.t }

func BenchmarkCounts(b *testing.B) {
	db, err := sqlite.Open(filepath.Join(b.TempDir(), "bench.db"), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	es := randomEntries(rand.New(rand.NewPCG(500, 1)), 500)
	var cs []gtd.Change
	for _, e := range es {
		cs = append(cs, gtd.Change{Op: gtd.OpCreate, Entry: e})
	}
	if err := db.Entries().Apply(context.Background(), cs); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := db.Entries().Counts(context.Background(), "OSS"); err != nil {
			b.Fatal(err)
		}
	}
}
