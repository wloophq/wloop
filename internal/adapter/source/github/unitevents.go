package ghsource

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// PRStates are the pull requests as the GitHub plugin last reported them.
type PRStates interface {
	PR(ctx context.Context, ref pr.Ref) (dinv.PR, bool, error)
}

// UnitEvents reads a pull request's stored reviews and merge, and its close
// from its reported state, as the resolver's events.
type UnitEvents struct {
	Store *inventory.Store
	PRs   PRStates
	// Fresh is how recently the plugin must have seen a closed pull request
	// for its close to count.
	Fresh time.Duration
	Now   func() time.Time
}

// UnitEvents are u's events at or after since, oldest first. A dismissal is
// no review. A close is the reported pull request's, while it is closed and
// seen within Fresh: the stored close events outlive a reopening, which
// records no event, and a report goes stale on one no sweep reads.
func (e UnitEvents) UnitEvents(ctx context.Context, u unit.Unit, since time.Time) ([]link.Event, error) {
	if u.Provider != unit.GitHub {
		return nil, fmt.Errorf("%s is not a pull request", u)
	}
	ref, err := pr.ParseKey(u.Key)
	if err != nil {
		return nil, err
	}
	evs, err := e.Store.Events(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []link.Event
	reported, ok, err := e.PRs.PR(ctx, ref)
	if err != nil {
		return nil, err
	}
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
		default:
			continue
		}
		out = append(out, link.Event{Unit: u, Kind: kind, Actor: actorKey(ev.Actor), At: ev.At,
			Anchor: unit.Anchor{Unit: u, Ref: anchor, At: ev.At}})
	}
	closed := ok && reported.State == "CLOSED" && !reported.ClosedAt.IsZero() && e.Now().Sub(reported.LastSeenAt) < e.Fresh
	if closed && !reported.ClosedAt.Before(since) {
		at := reported.ClosedAt
		out = append(out, link.Event{Unit: u, Kind: link.EventClosed, At: at, Anchor: unit.Anchor{Unit: u, Ref: "closed", At: at}})
		slices.SortStableFunc(out, func(a, b link.Event) int { return a.At.Compare(b.At) })
	}
	return out, nil
}

func actorKey(login string) string {
	if login == "" {
		return ""
	}
	return string(unit.GitHub) + ":" + login
}

// Refresher reads a pull request in full again when the plugin's last report
// of it may be stale: never reported, or not seen for Every. A merge is final,
// so a merged one is never read again.
type Refresher struct {
	PRs   PRStates
	Read  func(ctx context.Context, ref pr.Ref) error
	Every time.Duration
	Now   func() time.Time
}

func (r Refresher) Refresh(ctx context.Context, u unit.Unit) (bool, error) {
	if u.Provider != unit.GitHub {
		return false, fmt.Errorf("%s is not a pull request", u)
	}
	ref, err := pr.ParseKey(u.Key)
	if err != nil {
		return false, err
	}
	p, ok, err := r.PRs.PR(ctx, ref)
	if err != nil {
		return false, err
	}
	if ok && (p.State == "MERGED" || r.Now().Sub(p.LastSeenAt) < r.Every) {
		return false, nil
	}
	if err := r.Read(ctx, ref); err != nil {
		return false, err
	}
	return true, nil
}
