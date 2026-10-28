package collect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

// TrackMax bounds what every fast sweep reads by reference for tracking
// alone: three batches of InventoryByRef.
const TrackMax = 50

// UntrackFinished clears the track of pull requests a read showed merged or
// closed; from then on they are followed like any finished pull request.
func UntrackFinished(ctx context.Context, st *store.DB, now func() time.Time, logf func(string, ...any)) {
	tracked, err := st.Tracked(ctx)
	if err != nil {
		logf("untrack finished: %v", err)
		return
	}
	stored, err := st.PRs(ctx, tracked)
	if err != nil {
		logf("untrack finished: %v", err)
		return
	}
	for _, ref := range tracked {
		pull, ok := stored[ref]
		if !ok || pull.State == api.StateOpen {
			continue
		}
		changed, err := st.SetTracked(ctx, ref, false)
		if err == nil && changed {
			err = st.RecordEvents(ctx, []pr.Event{{PR: ref, Kind: pr.KindUntracked, At: now(), Data: strings.ToLower(pull.State)}})
		}
		if err != nil {
			logf("%s: untrack finished: %v", ref, err)
		}
	}
}

// Follows track and untrack the pull requests the user follows by hand, in
// two phases: a read, which stores nothing, and a follow, which admits the
// read and stores it.
type Follows struct {
	Store *store.DB
	// Read reads a pull request about to be tracked; nil tracks it unread.
	Read func(ctx context.Context, ref api.Ref) (Read, error)
	// Save stores a read.
	Save func(ctx context.Context, r Read) error
	Now  func() time.Time
	// Name is what the refusals call the loop.
	Name string

	// mu holds a track's admission and its write together.
	mu sync.Mutex
	// reads are the reads of the tracks not followed yet, by folded key.
	readsMu sync.Mutex
	reads   map[string]Read
}

// ReadTrack reads a pull request about to be tracked, so a nudge right
// after the track finds its situation, and names it as GitHub does. It
// refuses a new track past TrackMax. The read runs without the lock, so a
// slow read stalls no other track, and is kept for Follow.
func (f *Follows) ReadTrack(ctx context.Context, ref api.Ref) (src.Tracked, error) {
	known, tracked, err := f.slot(ctx, ref)
	if err != nil {
		return src.Tracked{}, err
	}
	out := src.Tracked{Unit: known.Unit()}
	if tracked || f.Read == nil {
		return out, nil
	}
	r, err := f.Read(ctx, ref)
	var low *api.BudgetError
	if errors.As(err, &low) {
		return out, fmt.Errorf("%w: track %s: %v; try again after the reset", src.ErrUnavailable, ref, low)
	}
	if err != nil {
		return out, err
	}
	f.readsMu.Lock()
	if f.reads == nil {
		f.reads = map[string]Read{}
	}
	f.reads[foldKey(r.Snapshot.Ref)] = r
	f.readsMu.Unlock()
	return src.Tracked{Unit: r.Snapshot.Ref.Unit(), State: r.Snapshot.State}, nil
}

// Follow tracks ref once its slot and its state allow it, storing the read
// ReadTrack kept of it first; a refused track leaves no row.
func (f *Follows) Follow(ctx context.Context, ref api.Ref) (src.Tracked, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	known, _, err := f.slot(ctx, ref)
	if err != nil {
		return src.Tracked{}, err
	}
	f.readsMu.Lock()
	r, read := f.reads[foldKey(known)]
	delete(f.reads, foldKey(known))
	f.readsMu.Unlock()
	state := ""
	if read {
		state = r.Snapshot.State
	} else {
		stored, ok, err := f.Store.PR(ctx, known)
		if err != nil {
			return src.Tracked{}, err
		}
		if ok {
			state = stored.State
		}
	}
	if state != "" && state != api.StateOpen {
		return src.Tracked{}, fmt.Errorf("%w: %s is %s; %s follows open pull requests", src.ErrUnfit, known, strings.ToLower(state), f.Name)
	}
	if read && f.Save != nil {
		if err := f.Save(ctx, r); err != nil {
			return src.Tracked{}, err
		}
	}
	changed, err := f.Store.SetTracked(ctx, known, true)
	if err == nil && changed {
		err = f.Store.RecordEvents(ctx, []pr.Event{{PR: known, Kind: pr.KindTracked, At: f.Now()}})
	}
	return src.Tracked{Unit: known.Unit(), State: state}, err
}

// Untrack stops tracking ref, the tracked pull request it names under any
// case.
func (f *Follows) Untrack(ctx context.Context, ref api.Ref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	tracked, err := f.Store.Tracked(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(tracked, func(t api.Ref) bool { return same(t, ref) })
	if i < 0 {
		return fmt.Errorf("%w: %s is not tracked", src.ErrUnfit, ref)
	}
	changed, err := f.Store.SetTracked(ctx, tracked[i], false)
	if err != nil || !changed {
		return err
	}
	return f.Store.RecordEvents(ctx, []pr.Event{{PR: tracked[i], Kind: pr.KindUntracked, At: f.Now()}})
}

// slot is the tracked pull request that matches ref under any case, and
// refuses a new one past TrackMax.
func (f *Follows) slot(ctx context.Context, ref api.Ref) (api.Ref, bool, error) {
	tracked, err := f.Store.Tracked(ctx)
	if err != nil {
		return ref, false, err
	}
	if i := slices.IndexFunc(tracked, func(t api.Ref) bool { return same(t, ref) }); i >= 0 {
		return tracked[i], true, nil
	}
	if len(tracked) >= TrackMax {
		names := make([]string, len(tracked))
		for i, t := range tracked {
			names[i] = t.String()
		}
		return ref, false, fmt.Errorf("%w: already tracking %d pull requests (%s); untrack one first", src.ErrUnfit, len(tracked), strings.Join(names, ", "))
	}
	return ref, false, nil
}

// same says a and b name one pull request: GitHub's names fold case.
func same(a, b api.Ref) bool { return foldKey(a) == foldKey(b) }

func foldKey(r api.Ref) string { return strings.ToLower(r.String()) }
