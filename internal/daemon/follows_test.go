package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// trackMax is the GitHub plugin's cap on the pull requests tracked by hand.
const trackMax = 50

// testFollows are the track and the untrack the GitHub plugin serves, over
// store, as the daemon's tests drive them: read reads a pull request about
// to be tracked, nil tracks it unread.
func testFollows(store *inventory.Store, read func(ctx context.Context, ref github.Ref) (port.TrackRead, error), units func() *unit.Registry,
	now func() time.Time) (track func(ctx context.Context, u unit.Unit) (port.TrackRead, error), untrack func(ctx context.Context, u unit.Unit) error) {
	f := &follows{store: store, read: read, units: units, now: now}
	return f.Track, f.Untrack
}

// follows tracks and untracks pull requests the user follows by hand.
type follows struct {
	store *inventory.Store
	read  func(ctx context.Context, ref github.Ref) (port.TrackRead, error)
	units func() *unit.Registry
	now   func() time.Time
	// mu holds a track's admission and its write together.
	mu sync.Mutex
}

// Track reads a pull request about to be tracked, so a nudge right after
// the track finds its situation, and names it as GitHub does. The read
// runs without the lock, so a slow read stalls no other track; Save checks
// the slot and the state again under it, and only then stores the read, so
// a refused track leaves no row.
func (f *follows) Track(ctx context.Context, u unit.Unit) (port.TrackRead, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return port.TrackRead{}, fmt.Errorf("%w: %s is not a pull request", port.ErrNotFound, u.Label())
	}
	known, tracked, err := f.slot(ctx, ref)
	if err != nil {
		return port.TrackRead{}, err
	}
	read := port.TrackRead{Unit: known.Unit()}
	if !tracked && f.read != nil {
		read, err = f.read(ctx, ref)
		var low *dgh.BudgetError
		if errors.As(err, &low) {
			return read, api.Refuse(http.StatusTooManyRequests, "track %s: %v; try again after the reset", u.Label(), low)
		}
		if err != nil {
			return read, err
		}
	}
	save := read.Save
	read.Save = func(ctx context.Context) error { return f.admit(ctx, read.Unit, read.State, save) }
	return read, nil
}

// admit tracks u once its slot and its state allow it, storing the read
// first.
func (f *follows) admit(ctx context.Context, u unit.Unit, state string, save func(context.Context) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, _ := pr.RefOf(u)
	known, _, err := f.slot(ctx, ref)
	if err != nil {
		return err
	}
	if state == "" {
		stored, ok, err := inventorytest.Of(f.store).PR(ctx, known)
		if err != nil {
			return err
		}
		if ok {
			state = stored.State
		}
	}
	if state != "" && state != dgh.StateOpen {
		return api.Refuse(http.StatusBadRequest, "%s is %s; "+brand.Name+" follows open pull requests", u.Label(), strings.ToLower(state))
	}
	if save != nil {
		if err := save(ctx); err != nil {
			return err
		}
	}
	changed, err := inventorytest.Of(f.store).SetTracked(ctx, known, true)
	if err != nil || !changed {
		return err
	}
	return f.store.RecordEvent(ctx, dinv.Event{PR: known, Kind: dinv.KindTracked, At: f.now()})
}

// Untrack stops tracking u, the tracked pull request it names under any
// case.
func (f *follows) Untrack(ctx context.Context, u unit.Unit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := pr.RefOf(u)
	if !ok {
		return api.Refuse(http.StatusBadRequest, "%s is not tracked", u.Label())
	}
	tracked, err := inventorytest.Of(f.store).Tracked(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(tracked, func(t github.Ref) bool { return f.same(t, ref) })
	if i < 0 {
		return api.Refuse(http.StatusBadRequest, "%s is not tracked", u.Label())
	}
	changed, err := inventorytest.Of(f.store).SetTracked(ctx, tracked[i], false)
	if err != nil || !changed {
		return err
	}
	return f.store.RecordEvent(ctx, dinv.Event{PR: tracked[i], Kind: dinv.KindUntracked, At: f.now()})
}

// slot is the tracked pull request that matches ref under any case, and
// refuses a new one past trackMax.
func (f *follows) slot(ctx context.Context, ref github.Ref) (github.Ref, bool, error) {
	tracked, err := inventorytest.Of(f.store).Tracked(ctx)
	if err != nil {
		return ref, false, err
	}
	if i := slices.IndexFunc(tracked, func(t github.Ref) bool { return f.same(t, ref) }); i >= 0 {
		return tracked[i], true, nil
	}
	if len(tracked) >= trackMax {
		names := make([]string, len(tracked))
		for i, t := range tracked {
			names[i] = t.String()
		}
		return ref, false, api.Refuse(http.StatusBadRequest, "already tracking %d pull requests (%s); untrack one first", len(tracked), strings.Join(names, ", "))
	}
	return ref, false, nil
}

func (f *follows) same(a, b github.Ref) bool {
	if f.units == nil {
		return a == b
	}
	return f.units().Same(a.Unit(), b.Unit())
}
