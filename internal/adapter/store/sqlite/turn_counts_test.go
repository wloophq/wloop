package sqlite_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTurnCountsAreTheBooksTurns(t *testing.T) {
	t.Parallel()
	f := storetest.Seeded(t, storetest.Live)
	ctx := context.Background()
	books, err := f.Turns.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	none := pr.Ref{Owner: "nobody", Repo: "nothing", Number: 1}.Unit()
	got, err := f.Turns.TurnCounts(ctx, append([]unit.Unit{none}, f.WithTurns...))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[none]; ok || len(got) != len(f.WithTurns) {
		t.Fatalf("counted %d units, want %d and none for %s", len(got), len(f.WithTurns), none)
	}
	for _, u := range f.WithTurns {
		if got[u] != len(books[u].Turns) {
			t.Errorf("%s: %d turns, the book has %d", u, got[u], len(books[u].Turns))
		}
	}
}

func TestOpenTurnsAreTheBooksOpenTurns(t *testing.T) {
	t.Parallel()
	f := storetest.Seeded(t, storetest.Live)
	ctx := context.Background()
	books, err := f.Turns.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Turns.OpenTurns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for u, b := range books {
		var open []turn.Turn
		for _, tr := range b.Turns {
			if tr.State.Open() {
				open = append(open, turn.Turn{Seq: tr.Seq, Intent: tr.Intent, Trigger: tr.Trigger, State: tr.State, Session: tr.Session, SentAt: tr.SentAt})
			}
		}
		want += len(open)
		if !reflect.DeepEqual(got[u], open) && !(len(got[u]) == 0 && len(open) == 0) {
			t.Errorf("%s: open turns %+v, the book has %+v", u, got[u], open)
		}
	}
	n := 0
	for _, ts := range got {
		n += len(ts)
	}
	if want == 0 || n != want {
		t.Fatalf("%d open turns, the books have %d", n, want)
	}
}

func TestTimelineIsTheBooksTurnsWithTheirTimes(t *testing.T) {
	t.Parallel()
	f := storetest.Seeded(t, storetest.Live)
	ctx := context.Background()
	books, err := f.Turns.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Turns.Timeline(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for u, b := range books {
		for i, tr := range b.Turns {
			want++
			if i >= len(got[u]) {
				t.Fatalf("%s: turn %d missing from the timeline", u, tr.Seq)
			}
			g := got[u][i]
			if g.Seq != tr.Seq || g.State != tr.State || !g.SentAt.Equal(tr.SentAt) || !g.StartedAt.Equal(tr.StartedAt) || !g.EndedAt.Equal(tr.EndedAt) {
				t.Errorf("%s: timeline turn %+v, the book has %+v", u, g, tr)
			}
		}
	}
	n := 0
	for _, ts := range got {
		n += len(ts)
	}
	if want == 0 || n != want {
		t.Fatalf("%d turns in the timeline, the books have %d", n, want)
	}
	if later, err := f.Turns.Timeline(ctx, time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	} else if open := countOpen(later); open == 0 || open != len(flatten(later)) {
		t.Fatalf("a timeline since the far future holds %d turns, %d open, want only the open ones", len(flatten(later)), open)
	}
}

func flatten(m map[unit.Unit][]turn.Turn) []turn.Turn {
	var out []turn.Turn
	for _, ts := range m {
		out = append(out, ts...)
	}
	return out
}

func countOpen(m map[unit.Unit][]turn.Turn) int {
	n := 0
	for _, tr := range flatten(m) {
		if tr.State.Open() {
			n++
		}
	}
	return n
}
