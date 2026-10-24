package testsink_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/view"
)

func TestSinkContract(t *testing.T) {
	viewstest.RunSink(t, func(*testing.T) (views.ViewSink, views.PlacementReader, viewstest.Board) {
		s := testsink.New("test")
		return s, s, s
	})
}

func TestSinkKeepsBatchesAndFailsWhenTold(t *testing.T) {
	s := testsink.New("test")
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	items := []view.Item{{Unit: ref, Role: view.ReviewRole, Status: view.Working}}
	if _, err := s.Apply(context.Background(), items, nil); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("board down")
	s.Fail(boom)
	if _, err := s.Apply(context.Background(), items, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if got := s.Batches(); len(got) != 2 || got[0][0] != items[0] {
		t.Fatalf("batches = %+v", got)
	}
	if !s.Manages(view.AuthorRole, "in-progress") || s.Manages(view.AuthorRole, "reviewed") {
		t.Fatal("author vocabulary")
	}
}
