package cli

import (
	"context"
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type countedBooks struct{ reads int }

func (b *countedBooks) Books(_ context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	b.reads++
	out := map[unit.Unit]turn.Book{}
	for _, u := range units {
		out[u] = turn.Book{}
	}
	return out, nil
}

func (b *countedBooks) BookOf(context.Context, turns.Key) (turn.Book, bool, error) {
	return turn.Book{}, false, nil
}

type noPRs struct{}

func (noPRs) PRs(context.Context, []pr.Ref) (map[pr.Ref]dinv.PR, error) { return nil, nil }

func TestAPageReadsEachUnitsBookOnce(t *testing.T) {
	t.Parallel()
	u := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	inner := &countedBooks{}
	b := openPRBooks{books: inner, prs: noPRs{}}
	ctx := coretasks.WithPage(context.Background())
	for range 3 {
		if _, err := b.Books(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if inner.reads != 1 {
		t.Errorf("%d reads of one unit's book in a page, want 1", inner.reads)
	}
	if _, err := b.Books(context.Background(), u); err != nil || inner.reads != 2 {
		t.Errorf("outside a page: %d reads, %v, want a read", inner.reads, err)
	}
}
