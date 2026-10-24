package ghsource

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// unitEvents reads a pull request's stored reviews, merge and close as the
// resolver's events.
type unitEvents struct{ store *inventory.Store }

// UnitEvents are u's events at or after since, oldest first. A dismissal is
// no review.
func (e unitEvents) UnitEvents(ctx context.Context, u unit.Unit, since time.Time) ([]link.Event, error) {
	if u.Provider != unit.GitHub {
		return nil, fmt.Errorf("%s is not a pull request", u)
	}
	ref, err := pr.ParseKey(u.Key)
	if err != nil {
		return nil, err
	}
	evs, err := e.store.Events(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []link.Event
	for _, ev := range evs {
		if ev.At.Before(since) {
			continue
		}
		var kind link.EventKind
		var anchor string
		switch {
		case ev.Kind == dinv.KindReview && !strings.HasPrefix(ev.Data, pr.ReviewDismissed+":"):
			kind, anchor = link.EventReview, "review:"+ev.Key
		case ev.Kind == dinv.KindMerged:
			kind, anchor = link.EventMerged, "merged"
		case ev.Kind == dinv.KindClosed:
			kind, anchor = link.EventClosed, "closed"
		default:
			continue
		}
		out = append(out, link.Event{Unit: u, Kind: kind, Actor: actorKey(ev.Actor), At: ev.At,
			Anchor: unit.Anchor{Unit: u, Ref: anchor, At: ev.At}})
	}
	return out, nil
}

func actorKey(login string) string {
	if login == "" {
		return ""
	}
	return string(unit.GitHub) + ":" + login
}
