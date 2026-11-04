package sqlite_test

import (
	"context"
	"reflect"
	"testing"

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
