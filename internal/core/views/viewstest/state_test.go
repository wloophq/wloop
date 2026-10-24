package viewstest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/view"
)

func TestFakeState(t *testing.T) {
	RunState(t, func(*testing.T) views.State { return NewState() }, func(_ *testing.T, s views.State) []port.Event { return s.(*State).Events() })
}

type recordingTB struct {
	testing.TB
	errs []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestStrictFakesWantEvents(t *testing.T) {
	ctx := context.Background()
	rec := &recordingTB{}
	s := NewState(Strict(rec))
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	a := view.Applied{Unit: pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit(), Role: view.ReviewRole, Status: view.Working, At: at}
	s.Put("orca", a)
	if err := s.SaveApplied(ctx, "orca", a, port.Event{Unit: a.Unit, Kind: views.KindViewApplied, At: at}); err != nil {
		t.Fatal(err)
	}
	if len(rec.errs) != 0 {
		t.Fatalf("a seed or a save with events failed the test: %v", rec.errs)
	}
	s.SaveApplied(ctx, "orca", a)
	if want := "[viewstest: State.SaveApplied without an event]"; fmt.Sprint(rec.errs) != want {
		t.Fatalf("errors %q, want %s", rec.errs, want)
	}
}
