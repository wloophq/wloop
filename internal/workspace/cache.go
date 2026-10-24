package workspace

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
)

var parts = [...]wsdomain.Part{wsdomain.PartRepos, wsdomain.PartWorkspaces, wsdomain.PartPanes, wsdomain.PartTerminals}

// loadTimeout bounds one load, so a hung manager call cannot hold a part's reads.
const loadTimeout = 30 * time.Second

// DefaultMaxAge is how long each part serves reads: repos 10 minutes, every mutable part
// the fast interval.
func DefaultMaxAge(fast time.Duration) map[wsdomain.Part]time.Duration {
	return map[wsdomain.Part]time.Duration{
		wsdomain.PartRepos:      10 * time.Minute,
		wsdomain.PartWorkspaces: fast,
		wsdomain.PartPanes:      fast,
		wsdomain.PartTerminals:  fast,
	}
}

// Cache serves snapshots of an Observer's listings, loading each part at most once per
// MaxAge and once per Invalidate, with concurrent reads of a part sharing one load.
type Cache struct {
	src    Observer
	maxAge map[wsdomain.Part]time.Duration
	now    func() time.Time

	mu sync.Mutex
	// last is never modified once published; a load builds the next one.
	last *wsdomain.Snapshot
	// epoch counts the Invalidates of each part; dataEpoch is the epoch the loaded data
	// started at, so the data is stale once they differ.
	epoch     map[wsdomain.Part]uint64
	dataEpoch map[wsdomain.Part]uint64
	inflight  map[wsdomain.Part]*load
	runtime   string
	// foreign is the parts last read from a runtime other than the current one.
	foreign  wsdomain.Part
	onChange func(*wsdomain.Snapshot, []string)
	base     context.Context

	notifyMu sync.Mutex
}

type load struct {
	parts wsdomain.Part
	epoch map[wsdomain.Part]uint64
	done  chan struct{}
	// failed is the parts the load could not read; err says why.
	failed wsdomain.Part
	err    error
	// restarted is set when the load saw another runtime and invalidated parts for it.
	restarted bool
}

func NewCache(src Observer, maxAge map[wsdomain.Part]time.Duration, now func() time.Time) *Cache {
	return &Cache{src: src, maxAge: maxAge, now: now,
		epoch: map[wsdomain.Part]uint64{}, dataEpoch: map[wsdomain.Part]uint64{}, inflight: map[wsdomain.Part]*load{},
		base: context.Background()}
}

// SetBase is the context the loads Refresh starts run under, such as one carrying the
// meter registry; each opens a loop:workspace scope in it.
func (c *Cache) SetBase(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base = ctx
}

// Read returns a snapshot holding at least want, each part read within its MaxAge and by
// a load that began after the latest Invalidate of it. On error the cache keeps its last
// snapshot. A runtime change costs one reload; if the reload sees another change, Read
// returns the snapshot as loaded, its scopes incomplete when their runtimes disagree, and
// the next Read loads again.
func (c *Cache) Read(ctx context.Context, want wsdomain.Part) (*wsdomain.Snapshot, error) {
	var joined wsdomain.Part
	restarts := 0
	for {
		c.mu.Lock()
		if restarts > 1 {
			s := c.last
			c.mu.Unlock()
			return partialAcrossRuntimes(s), nil
		}
		now := c.now()
		var need wsdomain.Part
		var waits []*load
		for _, p := range parts {
			if want&p == 0 || c.fresh(p, now, joined&p != 0) {
				continue
			}
			if l := c.inflight[p]; l != nil {
				if l.epoch[p] == c.epoch[p] {
					joined |= p
				}
				if !slices.Contains(waits, l) {
					waits = append(waits, l)
				}
				continue
			}
			need |= p
		}
		if need == 0 && len(waits) == 0 {
			s := c.last
			c.mu.Unlock()
			return s, nil
		}
		if need != 0 {
			waits = append(waits, c.start(ctx, need))
			joined |= need
		}
		c.mu.Unlock()
		for _, l := range waits {
			select {
			case <-l.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if l.failed&want != 0 {
				return nil, l.err
			}
			if l.restarted {
				restarts++
			}
		}
	}
}

// partialAcrossRuntimes is s with both scopes incomplete when they name two runtimes.
func partialAcrossRuntimes(s *wsdomain.Snapshot) *wsdomain.Snapshot {
	a, b := s.WorkspaceScope.RuntimeID, s.TerminalScope.RuntimeID
	if a == "" || b == "" || a == b {
		return s
	}
	cp := *s
	cp.WorkspaceScope.Complete, cp.TerminalScope.Complete = false, false
	return &cp
}

// fresh reports whether p's data serves a read: loaded, not invalidated since its load
// began, and within MaxAge unless the read waited on that very load.
func (c *Cache) fresh(p wsdomain.Part, now time.Time, joined bool) bool {
	if c.last == nil || c.last.Loaded&p == 0 || c.dataEpoch[p] != c.epoch[p] {
		return false
	}
	return joined || now.Sub(c.last.ReadAt[p]) < c.maxAge[p]
}

func (c *Cache) start(ctx context.Context, need wsdomain.Part) *load {
	l := &load{parts: need, epoch: map[wsdomain.Part]uint64{}, done: make(chan struct{})}
	for _, p := range parts {
		if need&p != 0 {
			l.epoch[p] = c.epoch[p]
			c.inflight[p] = l
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
	startedAt := c.now()
	go func() {
		defer cancel()
		c.run(ctx, l, startedAt)
	}()
	return l
}

type result struct {
	repos      []wsdomain.Repo
	workspaces []wsdomain.Workspace
	panes      []wsdomain.Pane
	terminals  []wsdomain.Terminal
	wsScope    wsdomain.Scope
	termScope  wsdomain.Scope
	ok         wsdomain.Part
}

func (c *Cache) run(ctx context.Context, l *load, startedAt time.Time) {
	r, err := c.fetch(ctx, l.parts)
	c.mu.Lock()
	for _, p := range parts {
		if c.inflight[p] == l {
			delete(c.inflight, p)
		}
	}
	next, moved, changed, restarted := c.commit(l, r, startedAt)
	fn := c.onChange
	c.mu.Unlock()
	l.failed, l.err = l.parts&^r.ok, err
	l.restarted = restarted
	if changed && fn != nil {
		c.notifyMu.Lock()
		fn(next, moved)
		c.notifyMu.Unlock()
	}
	close(l.done)
}

func (c *Cache) fetch(ctx context.Context, need wsdomain.Part) (result, error) {
	var r result
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	do := func(p wsdomain.Part, f func() error) {
		if need&p == 0 {
			return
		}
		wg.Go(func() {
			err := f()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			r.ok |= p
		})
	}
	do(wsdomain.PartRepos, func() (err error) {
		r.repos, err = c.src.Repos(ctx)
		return err
	})
	do(wsdomain.PartWorkspaces, func() (err error) {
		r.workspaces, r.wsScope, err = c.src.Workspaces(ctx)
		return err
	})
	do(wsdomain.PartPanes, func() (err error) {
		r.panes, err = c.src.Panes(ctx)
		return err
	})
	do(wsdomain.PartTerminals, func() (err error) {
		r.terminals, r.termScope, err = c.src.AllTerminals(ctx)
		return err
	})
	wg.Wait()
	return r, errors.Join(errs...)
}

// commit publishes the parts r holds and reports the workspace ids whose projection moved
// and whether the load saw a runtime change.
// Called with c.mu held.
func (c *Cache) commit(l *load, r result, startedAt time.Time) (next *wsdomain.Snapshot, moved []string, changed, restarted bool) {
	if r.ok == 0 {
		return c.last, nil, false, false
	}
	next = &wsdomain.Snapshot{ReadAt: map[wsdomain.Part]time.Time{}}
	if c.last != nil {
		*next = *c.last
		next.ReadAt = maps.Clone(c.last.ReadAt)
	}
	var prev wsdomain.Snapshot
	if c.last != nil {
		prev = *c.last
	}
	if r.ok&wsdomain.PartRepos != 0 {
		changed = changed || !slices.Equal(prev.Repos, r.repos)
		next.Repos = r.repos
	}
	if r.ok&wsdomain.PartWorkspaces != 0 {
		moved = append(moved, movedIDs(prev.Workspaces, r.workspaces,
			func(w wsdomain.Workspace) string { return w.ID }, func(a, b wsdomain.Workspace) bool { return a == b })...)
		next.Workspaces, next.WorkspaceScope = r.workspaces, r.wsScope
	}
	if r.ok&wsdomain.PartPanes != 0 {
		moved = append(moved, movedIDs(prev.Panes, r.panes,
			func(p wsdomain.Pane) string { return p.WorkspaceID }, samePane)...)
		next.Panes = r.panes
	}
	if r.ok&wsdomain.PartTerminals != 0 {
		moved = append(moved, movedIDs(prev.Terminals, r.terminals,
			func(t wsdomain.Terminal) string { return t.WorkspaceID }, sameTerminal)...)
		next.Terminals, next.TerminalScope = r.terminals, r.termScope
	}
	slices.Sort(moved)
	moved = slices.Compact(moved)
	changed = changed || len(moved) > 0
	for _, p := range parts {
		if r.ok&p != 0 {
			next.Loaded |= p
			next.ReadAt[p] = startedAt
			c.dataEpoch[p] = l.epoch[p]
		}
	}
	c.foreign &^= r.ok
	var split bool
	restarted, split = c.noteRuntime(r)
	if split {
		next.WorkspaceScope.Complete, next.TerminalScope.Complete = false, false
	}
	if changed {
		next.Gen++
	}
	c.last = next
	return next, moved, changed, restarted
}

// noteRuntime invalidates every part a load did not reread when the manager answering it
// is another instance than the one before, so no snapshot mixes two runtimes. It reports
// whether it invalidated, and split when one load's listings named two runtimes.
func (c *Cache) noteRuntime(r result) (restarted, split bool) {
	var ids []string
	if r.ok&wsdomain.PartWorkspaces != 0 && r.wsScope.RuntimeID != "" {
		ids = append(ids, r.wsScope.RuntimeID)
	}
	if r.ok&wsdomain.PartTerminals != 0 && r.termScope.RuntimeID != "" {
		ids = append(ids, r.termScope.RuntimeID)
	}
	if len(ids) == 0 {
		return false, false
	}
	split = len(ids) == 2 && ids[0] != ids[1]
	restarted = c.runtime != "" && slices.ContainsFunc(ids, func(id string) bool { return id != c.runtime })
	c.runtime = ids[len(ids)-1]
	if !split && !restarted {
		return false, false
	}
	for _, p := range parts {
		if split || r.ok&p == 0 {
			c.epoch[p]++
			c.foreign |= p
		}
	}
	return true, split
}

// movedIDs is the keys whose items differ between old and cur, items grouped by key in
// listing order.
func movedIDs[T any](old, cur []T, key func(T) string, same func(a, b T) bool) []string {
	group := func(xs []T) map[string][]T {
		out := map[string][]T{}
		for _, x := range xs {
			out[key(x)] = append(out[key(x)], x)
		}
		return out
	}
	a, b := group(old), group(cur)
	var out []string
	for k, xs := range a {
		if !slices.EqualFunc(xs, b[k], same) {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

func samePane(a, b wsdomain.Pane) bool {
	return a.WorkspaceID == b.WorkspaceID && a.Pane == b.Pane && a.State == b.State &&
		a.StateSince.Equal(b.StateSince) && a.UserActive == b.UserActive
}

func sameTerminal(a, b wsdomain.Terminal) bool {
	a.LastOutput, b.LastOutput = time.Time{}, time.Time{}
	return a == b
}

// Invalidate marks parts stale: the next Read reloads them. It never blocks on a load.
func (c *Cache) Invalidate(p wsdomain.Part) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, q := range parts {
		if p&q != 0 {
			c.epoch[q]++
		}
	}
}

// Refresh starts one asynchronous reload of p, after an Invalidate of it, and returns at
// once; OnChange fires if a projection moved.
func (c *Cache) Refresh(p wsdomain.Part) {
	c.Invalidate(p)
	c.mu.Lock()
	base := c.base
	c.mu.Unlock()
	go func() {
		ctx, sc := meter.Start(base, meter.Loop("workspace"))
		defer sc.End()
		_, _ = c.Read(ctx, p)
	}()
}

// Last is the latest snapshot without asking the manager; nil before the first load.
func (c *Cache) Last() *wsdomain.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Recent is the latest snapshot when p was read by the current runtime within twice its
// MaxAge (with no MaxAge, at any age), nil otherwise; it never asks the manager.
func (c *Cache) Recent(p wsdomain.Part) *wsdomain.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.last
	if s == nil || s.Loaded&p == 0 || c.foreign&p != 0 {
		return nil
	}
	if age := c.maxAge[p]; age > 0 && c.now().Sub(s.ReadAt[p]) >= 2*age {
		return nil
	}
	return s
}

// OnChange sets fn to be called after each load that changed a projection, with the ids
// of the workspaces whose part moved, before the reads waiting on that load return. fn
// must not wait on a load of this cache (Last is safe), and two loads of disjoint parts may
// call it out of order, so fn should read the newest snapshot from Last.
func (c *Cache) OnChange(fn func(s *wsdomain.Snapshot, moved []string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onChange = fn
}
