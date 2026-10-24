// Package viewstest holds the contracts every views port implementation
// passes, and the fakes the core's tests use.
package viewstest

import (
	"context"
	"github.com/wloophq/wloop/internal/domain/unit"
	"testing"

	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/view"
)

// Board is the sink's side the suite drives: it places a card the way the
// user would and reads it back.
type Board interface {
	// Place puts the pull request's review card in column.
	Place(ref unit.Unit, column string)
	Card(ref unit.Unit) (column, comment string, unread bool)
	// Column is the column the sink writes for status.
	Column(status view.Status) string
	// Unmanaged is a column the sink does not manage, as the sink reports it.
	Unmanaged() string
}

// RunSink checks a ViewSink and its PlacementReader against the placement
// rules: inside the managed vocabulary, or forced, the item's column is
// written; outside it the user's column stays and only the summary and unread
// flow.
func RunSink(t *testing.T, newSink func(t *testing.T) (views.ViewSink, views.PlacementReader, Board)) {
	ctx := context.Background()
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	asks := view.Item{Unit: ref, Role: view.ReviewRole, Status: view.NeedsMe, Unread: true, Summary: "asks: keep both columns?"}

	apply := func(t *testing.T, s views.ViewSink, force map[unit.Unit]bool, items ...view.Item) []view.Applied {
		t.Helper()
		got, err := s.Apply(ctx, items, force)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	wantCard := func(t *testing.T, b Board, column, comment string, unread bool) {
		t.Helper()
		c, m, u := b.Card(ref)
		if c != column || m != comment || u != unread {
			t.Fatalf("card = (%q, %q, %v), want (%q, %q, %v)", c, m, u, column, comment, unread)
		}
	}

	t.Run("names itself", func(t *testing.T) {
		s, _, _ := newSink(t)
		if s.Name() == "" {
			t.Fatal("empty name")
		}
	})

	t.Run("manages the columns it writes", func(t *testing.T) {
		s, _, b := newSink(t)
		for _, st := range []view.Status{view.Working, view.NeedsMe, view.Reviewed, view.Done} {
			if !s.Manages(view.ReviewRole, b.Column(st)) {
				t.Errorf("review column %q of %s is not managed", b.Column(st), st)
			}
		}
		if s.Manages(view.ReviewRole, b.Unmanaged()) || s.Manages(view.ReviewRole, "") {
			t.Error("manages a column it never writes")
		}
	})

	t.Run("an item in a managed column is written", func(t *testing.T) {
		s, _, b := newSink(t)
		b.Place(ref, b.Column(view.Working))
		got := apply(t, s, nil, asks)
		wantCard(t, b, b.Column(view.NeedsMe), asks.Summary, true)
		if len(got) != 1 || got[0].Unit != ref || got[0].Status != view.NeedsMe || !got[0].Unread || got[0].Summary != asks.Summary || got[0].Override != "" {
			t.Fatalf("applied = %+v", got)
		}
	})

	t.Run("an item without a column is written", func(t *testing.T) {
		s, _, b := newSink(t)
		b.Place(ref, "")
		apply(t, s, nil, asks)
		wantCard(t, b, b.Column(view.NeedsMe), asks.Summary, true)
	})

	t.Run("an item in an unmanaged column keeps it and gets the comment", func(t *testing.T) {
		s, _, b := newSink(t)
		b.Place(ref, b.Unmanaged())
		got := apply(t, s, nil, asks)
		wantCard(t, b, b.Unmanaged(), asks.Summary, true)
		if len(got) != 1 || got[0].Override != b.Unmanaged() || got[0].Status != view.NeedsMe {
			t.Fatalf("applied = %+v", got)
		}
	})

	t.Run("a forced item is written from any column", func(t *testing.T) {
		s, _, b := newSink(t)
		b.Place(ref, b.Unmanaged())
		got := apply(t, s, map[unit.Unit]bool{ref: true}, asks)
		wantCard(t, b, b.Column(view.NeedsMe), asks.Summary, true)
		if len(got) != 1 || got[0].Override != "" {
			t.Fatalf("applied = %+v", got)
		}
	})

	t.Run("an item with no status writes nothing", func(t *testing.T) {
		s, _, b := newSink(t)
		b.Place(ref, b.Column(view.Reviewed))
		column, comment, unread := b.Card(ref)
		got := apply(t, s, map[unit.Unit]bool{ref: true}, view.Item{Unit: ref, Role: view.ReviewRole, Summary: "nothing"})
		wantCard(t, b, column, comment, unread)
		if len(got) != 0 {
			t.Fatalf("applied = %+v", got)
		}
	})

	t.Run("a placement read after a write returns the written column", func(t *testing.T) {
		s, p, b := newSink(t)
		b.Place(ref, b.Column(view.Working))
		apply(t, s, nil, view.Item{Unit: ref, Role: view.ReviewRole, Status: view.Reviewed, Summary: "commented"})
		got, err := p.Placements(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got[ref] != b.Column(view.Reviewed) {
			t.Fatalf("placements = %v, want %s in %q", got, ref, b.Column(view.Reviewed))
		}
	})
}
