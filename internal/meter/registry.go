package meter

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Registry keeps totals per scope full name since it was made. Safe for
// concurrent use.
type Registry struct {
	now   func() time.Time
	since time.Time

	mu      sync.Mutex
	entries map[string]*entry
	// marks are copies of the totals, one a markEvery while samples arrive,
	// kept markKeep: Recent subtracts one from the totals.
	marks    []mark
	markedAt time.Time
}

const (
	markEvery = time.Minute
	markKeep  = 30 * time.Minute
)

type mark struct {
	at     time.Time
	totals map[string]Total
}

type entry struct {
	mu       sync.Mutex
	runs     int64
	wall     time.Duration
	tally    Tally
	last     Tally
	lastAt   time.Time
	lastWall time.Duration
}

// NewRegistry makes a registry whose clock is now; a nil now is time.Now.
func NewRegistry(now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	since := now()
	return &Registry{now: now, since: since, entries: map[string]*entry{}, marks: []mark{{at: since}}, markedAt: since}
}

// WithRegistry returns ctx carrying r; a nil r returns ctx.
func WithRegistry(ctx context.Context, r *Registry) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, registryKey, r)
}

// Since is when the registry was made.
func (r *Registry) Since() time.Time { return r.since }

// Totals is one entry per scope full name that got a sample or ended,
// sorted by Name. Tallies are copies.
func (r *Registry) Totals() []Total {
	r.mu.Lock()
	names := make([]string, 0, len(r.entries))
	entries := make([]*entry, 0, len(r.entries))
	for name, e := range r.entries {
		names = append(names, name)
		entries = append(entries, e)
	}
	r.mu.Unlock()
	out := make([]Total, 0, len(names))
	for i, e := range entries {
		e.mu.Lock()
		if e.runs > 0 || len(e.tally) > 0 {
			out = append(out, Total{
				Name:     names[i],
				Runs:     e.runs,
				Wall:     e.wall,
				Tally:    e.tally.Merge(nil),
				Last:     copyOrNil(e.last),
				LastAt:   e.lastAt,
				LastWall: e.lastWall,
			})
		}
		e.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Total is a registry's totals for one scope full name.
type Total struct {
	Name     string
	Runs     int64         // scopes of this name ended
	Wall     time.Duration // summed over ended scopes
	Tally    Tally         // every sample since Since, ended scopes or not
	Last     Tally         // the last ended scope's tally; nil before one ends
	LastAt   time.Time
	LastWall time.Duration
}

func (r *Registry) entry(name string) *entry {
	now := r.now()
	r.mu.Lock()
	e, ok := r.entries[name]
	if !ok {
		e = &entry{tally: Tally{}}
		r.entries[name] = e
	}
	due := now.Sub(r.markedAt) >= markEvery
	if due {
		r.markedAt = now
	}
	r.mu.Unlock()
	if due {
		r.mark(now)
	}
	return e
}

func (r *Registry) mark(now time.Time) {
	totals := map[string]Total{}
	for _, t := range r.Totals() {
		totals[t.Name] = t
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.marks = append(r.marks, mark{now, totals})
	for len(r.marks) > 1 && !r.marks[1].at.After(now.Add(-markKeep)) {
		r.marks = r.marks[1:]
	}
}

// Recent is what each scope spent since the newest mark at least d old, and
// that mark's time; before one exists, since the registry was made. Runs,
// Wall and Tally are the window's; Last and its fields are the totals'.
func (r *Registry) Recent(d time.Duration) (time.Time, []Total) {
	totals := r.Totals()
	cut := r.now().Add(-d)
	r.mu.Lock()
	base := r.marks[0]
	for _, m := range r.marks {
		if !m.at.After(cut) {
			base = m
		}
	}
	r.mu.Unlock()
	var out []Total
	for _, t := range totals {
		b := base.totals[t.Name]
		t.Runs -= b.Runs
		t.Wall -= b.Wall
		t.Tally = t.Tally.Sub(b.Tally)
		if t.Runs > 0 || len(t.Tally) > 0 {
			out = append(out, t)
		}
	}
	return base.at, out
}

func (e *entry) record(x Sample, spend bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := Key{x.Res, x.Op}
	st := e.tally[k]
	if spend {
		st.Points += x.Points
	} else {
		st.Add(x)
	}
	e.tally[k] = st
}

func (e *entry) ended(last Tally, at time.Time, wall time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.runs++
	e.wall += wall
	e.last = last
	e.lastAt = at
	e.lastWall = wall
}

func copyOrNil(t Tally) Tally {
	if t == nil {
		return nil
	}
	return t.Merge(nil)
}
