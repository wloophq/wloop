package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// fakeCards targets the pull requests in carded and records each prefetch.
type fakeCards struct {
	carded     []unit.Unit
	prefetched [][]unit.Unit
}

func (c *fakeCards) Carded(context.Context) ([]unit.Unit, error) { return c.carded, nil }

func (c *fakeCards) Prefetch(_ context.Context, units []unit.Unit) error {
	c.prefetched = append(c.prefetched, slices.Clone(units))
	return nil
}

func TestTheViewsListThePullRequestsACardCanMoveFor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	withTurn, carded, closed := pr.Ref{Owner: "acme", Repo: "api", Number: 7}, pr.Ref{Owner: "acme", Repo: "api", Number: 8}, pr.Ref{Owner: "acme", Repo: "api", Number: 9}
	for _, r := range []pr.Ref{withTurn, carded, closed} {
		if err := inventorytest.Of(te.Store).UpsertPR(ctx, dinv.PR{Ref: r, Author: "me", State: dgh.StateOpen, LastSeenAt: testNow}); err != nil {
			t.Fatal(err)
		}
	}
	saveReview(t, te, withTurn, "automated")
	cards := &fakeCards{carded: []unit.Unit{carded.Unit()}}
	b := trackedBooks{books: te.repo, cards: cards}
	all, err := b.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all[withTurn.Unit()].Turns) != 1 {
		t.Fatalf("the book with a turn: %+v", all[withTurn.Unit()])
	}
	if got, ok := all[carded.Unit()]; !ok || got.Unit != carded.Unit() || len(got.Turns) != 0 {
		t.Fatalf("the carded pull request without a turn: %+v %v", got, ok)
	}
	if _, ok := all[closed.Unit()]; ok {
		t.Fatal("a pull request no card can move for is listed")
	}
	if len(cards.prefetched) != 1 || !slices.Equal(cards.prefetched[0], []unit.Unit{withTurn.Unit(), carded.Unit()}) {
		t.Fatalf("prefetched %v", cards.prefetched)
	}
	named, err := b.Books(ctx, closed.Unit())
	if err != nil || named[closed.Unit()].Unit != closed.Unit() {
		t.Fatalf("a named pull request without a turn: %+v %v", named, err)
	}
}
