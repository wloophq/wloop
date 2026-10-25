package sqlite_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestBooksAtLiveSizes(t *testing.T) {
	t.Parallel()
	f := storetest.Seeded(t, storetest.Live)
	ctx, sc := metertest.Start(t, context.Background())
	books, err := f.Turns.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != storetest.Live.TurnPRs {
		t.Fatalf("%d books, want %d", len(books), storetest.Live.TurnPRs)
	}
	sql := metertest.Limit{Res: meter.SQL}
	metertest.Within(t, sc, metertest.Budget{sql: 3})
	if got := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "read"}); got != 1 {
		t.Errorf("Books() opened %d read transactions, want 1", got)
	}
	if got := metertest.Count(sc, sql); got != 3 {
		t.Errorf("Books issued %d SQL statements, want exactly 3\n%s", got, metertest.Report(sc.Tally()))
	}
}

func TestBooksQueryBudget(t *testing.T) {
	t.Parallel()
	f := storetest.Seeded(t, storetest.Live)
	units := f.WithTurns[:20]
	ctx, sc := metertest.Start(t, context.Background())
	books, err := f.Turns.Books(ctx, units...)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 20 {
		t.Fatalf("%d books, want 20", len(books))
	}
	sql := metertest.Limit{Res: meter.SQL}
	if got := metertest.Count(sc, sql); got != 3 {
		t.Errorf("Books(20 units) issued %d SQL statements, want 3\n%s", got, metertest.Report(sc.Tally()))
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "read"}); got != 1 {
		t.Errorf("Books(20 units) opened %d read transactions, want 1", got)
	}
}

func TestBooksOfOneUnitEqualTheBookInBooks(t *testing.T) {
	t.Parallel()
	f := storetest.Seeded(t, storetest.Live)
	ctx := context.Background()
	all, err := f.Turns.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range f.WithTurns {
		one, err := f.Turns.Books(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(one[u], all[u]) {
			t.Errorf("%s: Books(unit) differs from Books()", u)
		}
	}
	none := pr.Ref{Owner: "nobody", Repo: "nothing", Number: 1}.Unit()
	got, err := f.Turns.Books(ctx, f.WithTurns[0], none, f.WithTurns[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !reflect.DeepEqual(got[none], turnBook(none)) {
		t.Errorf("Books with a duplicate and an unknown unit = %d books, unknown %+v", len(got), got[none])
	}
}

func turnBook(u unit.Unit) turn.Book { return turn.Book{Unit: u} }
