package cli

import (
	"context"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// inventoryFacts reads my reviews from the review events the collector
// stored; before it knows the viewer, it finds none. A review's key is its
// node id, which names no review URL, so each points to the pull request.
type inventoryFacts struct{ store *inventory.Store }

var _ turns.GitHub = inventoryFacts{}

func (f inventoryFacts) MyReviews(ctx context.Context, u unit.Unit, since time.Time) ([]turns.MyReview, error) {
	ref := prOf(u)
	viewer, err := f.store.Viewer(ctx, unit.GitHub)
	if err != nil || viewer == "" {
		return nil, err
	}
	evs, err := f.store.Events(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []turns.MyReview
	for _, ev := range evs {
		if ev.Kind != dinv.KindReview || !strings.EqualFold(ev.Actor, viewer) || ev.At.Before(since) {
			continue
		}
		state, _, _ := strings.Cut(ev.Data, ":")
		switch state {
		case pr.ReviewApproved, pr.ReviewChangesRequested, pr.ReviewCommented:
			out = append(out, turns.MyReview{ID: ev.Key, State: state, URL: ref.URL(), At: ev.At})
		}
	}
	return out, nil
}
