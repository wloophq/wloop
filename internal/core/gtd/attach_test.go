package gtd_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAttachAppendsAUnitOnceInAttachOrder(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e := r.put(t, gtd.Entry{ID: "e-1", Kind: gtd.KindCapture, State: gtd.Next, Title: "ship it", Origin: gtd.OriginUser})
	post := unit.Unit{Provider: "slack", Key: "T1:C1:100.1"}
	for _, u := range []unit.Unit{unitU, post, unitU} {
		if err := r.svc.Attach(ctx, e.ID, u); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.entry(t, e.ID).Units; !slices.Equal(got, []unit.Unit{unitU, post}) {
		t.Fatalf("units = %v, want the pull request, then the post", got)
	}
}

func TestAttachRefusesTheEntryItselfAndAMissingEntry(t *testing.T) {
	r := newRig()
	ctx := context.Background()
	e := r.put(t, gtd.Entry{ID: "e-1", Kind: gtd.KindCapture, State: gtd.Next, Title: "ship it", Origin: gtd.OriginUser})
	if err := r.svc.Attach(ctx, e.ID, gtd.UnitOf(e.ID)); !errors.Is(err, coregtd.ErrArgs) {
		t.Fatalf("attaching the entry to itself: err = %v, want ErrArgs", err)
	}
	if err := r.svc.Attach(ctx, e.ID, unit.Unit{}); !errors.Is(err, coregtd.ErrArgs) {
		t.Fatalf("attaching no unit: err = %v, want ErrArgs", err)
	}
	if err := r.svc.Attach(ctx, "e-gone", unitU); !errors.Is(err, coregtd.ErrNoEntry) {
		t.Fatalf("attaching to a missing entry: err = %v, want ErrNoEntry", err)
	}
	if got := r.entry(t, e.ID).Units; len(got) != 0 {
		t.Fatalf("units = %v, want none", got)
	}
}
