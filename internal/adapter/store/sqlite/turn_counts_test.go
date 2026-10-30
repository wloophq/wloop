package sqlite_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/domain/pr"
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
