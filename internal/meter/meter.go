// Package meter counts what a unit of the loop's work costs at its boundaries.
// A unit is a Scope carried by a context; adapters add a Sample per boundary
// call, and a Registry on the context keeps totals per scope since start.
package meter

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Resource is the boundary a sample crossed, named by port.
type Resource string

const (
	SQL       Resource = "sql"       // one statement; Op "query" or "exec"; Rows for a query
	Tx        Resource = "tx"        // one transaction, Dur from begin to commit or rollback; Op "read" (no exec ran) or "write"
	Workspace Resource = "workspace" // one workspace-manager call; Op its subcommand or method, such as "worktree ps"
	GitHub    Resource = "github"    // one GitHub API request; Op "graphql", "rest" or "gh <subcommand>"
	Git       Resource = "git"       // one git command; Op its subcommand
	Agent     Resource = "agent"     // one headless agent run; Op "ask" or "shell"
	Work      Resource = "work"      // one in-process unit its package names, such as "derive rows"
)

// Resources is every Resource in display order.
var Resources = []Resource{SQL, Tx, Workspace, GitHub, Git, Agent, Work}

// Sample is one call across a boundary.
type Sample struct {
	Res Resource
	// Op is bounded: a subcommand, a method or a statement class; never an
	// id, a ref, a path or a URL.
	Op    string
	Spawn bool // the call started a process
	Dur   time.Duration
	// Err is a failure other than the caller's cancellation or deadline:
	// adapters set it only when err != nil && ctx.Err() == nil.
	Err         bool
	NotModified bool  // a conditional request answered 304
	Points      int64 // GitHub rate-limit points the call spent, when the response says
	Bytes       int64 // response bytes read
	Rows        int64 // rows a query returned
	// Detail is the statement text (whitespace collapsed) or the argv, cut
	// to 300 bytes. Adapters fill it only when Tracing(ctx).
	Detail string
}

// Key names one operation of one resource.
type Key struct {
	Res Resource
	Op  string // "" in a per-resource roll-up
}

// Stat aggregates samples of one Key. Calls counts samples; Spend adds
// Points without a call.
type Stat struct {
	Calls, Spawns, Errors, NotModified int64
	Points, Bytes, Rows                int64
	Dur, Max                           time.Duration
}

// Add counts x as one call.
func (s *Stat) Add(x Sample) {
	s.Calls++
	if x.Spawn {
		s.Spawns++
	}
	if x.Err {
		s.Errors++
	}
	if x.NotModified {
		s.NotModified++
	}
	s.Points += x.Points
	s.Bytes += x.Bytes
	s.Rows += x.Rows
	s.Dur += x.Dur
	s.Max = max(s.Max, x.Dur)
}

// Merge is the sum of s and o.
func (s Stat) Merge(o Stat) Stat {
	return Stat{
		Calls:       s.Calls + o.Calls,
		Spawns:      s.Spawns + o.Spawns,
		Errors:      s.Errors + o.Errors,
		NotModified: s.NotModified + o.NotModified,
		Points:      s.Points + o.Points,
		Bytes:       s.Bytes + o.Bytes,
		Rows:        s.Rows + o.Rows,
		Dur:         s.Dur + o.Dur,
		Max:         max(s.Max, o.Max),
	}
}

// Tally is stats by Key: a value, safe to keep and compare.
type Tally map[Key]Stat

// Total is every op of res.
func (t Tally) Total(res Resource) Stat {
	var out Stat
	for k, s := range t {
		if k.Res == res {
			out = out.Merge(s)
		}
	}
	return out
}

// ByResource folds every op into Op "".
func (t Tally) ByResource() Tally {
	out := Tally{}
	for k, s := range t {
		k.Op = ""
		out[k] = out[k].Merge(s)
	}
	return out
}

// Merge is a new tally holding t and o.
func (t Tally) Merge(o Tally) Tally {
	out := make(Tally, len(t)+len(o))
	for k, s := range t {
		out[k] = s
	}
	for k, s := range o {
		out[k] = out[k].Merge(s)
	}
	return out
}

// Sub is t less o, key by key, without the keys left with nothing; Max
// stays t's.
func (t Tally) Sub(o Tally) Tally {
	out := Tally{}
	for k, s := range t {
		b := o[k]
		d := Stat{Calls: s.Calls - b.Calls, Spawns: s.Spawns - b.Spawns, Errors: s.Errors - b.Errors, NotModified: s.NotModified - b.NotModified,
			Points: s.Points - b.Points, Bytes: s.Bytes - b.Bytes, Rows: s.Rows - b.Rows, Dur: s.Dur - b.Dur, Max: s.Max}
		if d.Calls != 0 || d.Points != 0 {
			out[k] = d
		}
	}
	return out
}

// Keys is the tally's keys in Resources order, then by Op.
func (t Tally) Keys() []Key {
	rank := make(map[Resource]int, len(Resources))
	for i, r := range Resources {
		rank[r] = i
	}
	keys := make([]Key, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, iok := rank[keys[i].Res]
		rj, jok := rank[keys[j].Res]
		switch {
		case iok != jok:
			return iok
		case ri != rj:
			return ri < rj
		case keys[i].Res != keys[j].Res:
			return keys[i].Res < keys[j].Res
		}
		return keys[i].Op < keys[j].Op
	})
	return keys
}

// Scope is one unit of work. Safe for concurrent Add.
type Scope struct {
	name   string
	parent *Scope
	entry  *entry
	now    func() time.Time
	start  time.Time
	traced bool

	mu      sync.Mutex
	ended   bool
	end     time.Time
	tally   Tally
	final   Tally
	samples []Sample
}

type ctxKey int

const (
	scopeKey ctxKey = iota
	registryKey
	traceKey
)

// Start opens a scope under ctx's scope, if any. Its full name is
// name, or "<parent full name>/<name>" when nested. Samples added under the
// returned context reach this scope and every enclosing one, and reach the
// registry on ctx, if any, once, under this scope's full name.
func Start(ctx context.Context, name string) (context.Context, *Scope) {
	s := &Scope{name: name, now: time.Now, traced: Tracing(ctx), tally: Tally{}}
	if p, ok := ctx.Value(scopeKey).(*Scope); ok {
		s.parent = p
		s.name = p.name + "/" + name
	}
	if r, ok := ctx.Value(registryKey).(*Registry); ok {
		s.now = r.now
		s.entry = r.entry(s.name)
	}
	s.start = s.now()
	return context.WithValue(ctx, scopeKey, s), s
}

// Name is the scope's full name.
func (s *Scope) Name() string { return s.name }

// Tally is the scope's tally so far.
func (s *Scope) Tally() Tally {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return s.final.Merge(nil)
	}
	return s.tally.Merge(nil)
}

// End freezes the tally and counts one run of the scope's name in the
// registry, with its wall time and tally as the name's Last. A second End
// returns the same tally. Samples after End still reach the registry, not
// the tally.
func (s *Scope) End() Tally {
	s.mu.Lock()
	if s.ended {
		defer s.mu.Unlock()
		return s.final.Merge(nil)
	}
	s.ended = true
	s.end = s.now()
	s.final = s.tally
	s.tally = nil
	wall := s.end.Sub(s.start)
	final := s.final
	s.mu.Unlock()
	if s.entry != nil {
		s.entry.ended(final.Merge(nil), s.end, wall)
	}
	return final.Merge(nil)
}

// Samples are the samples in arrival order when the scope traces, nil
// otherwise.
func (s *Scope) Samples() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.traced {
		return nil
	}
	return append([]Sample(nil), s.samples...)
}

func (s *Scope) record(x Sample, spend bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	k := Key{x.Res, x.Op}
	st := s.tally[k]
	if spend {
		st.Points += x.Points
	} else {
		st.Add(x)
		if s.traced {
			s.samples = append(s.samples, x)
		}
	}
	s.tally[k] = st
}

// Trace makes every scope opened under ctx keep its samples, and adapters
// fill Detail. For tests; production never traces.
func Trace(ctx context.Context) context.Context {
	return context.WithValue(ctx, traceKey, true)
}

// Tracing reports whether ctx traces.
func Tracing(ctx context.Context) bool {
	return ctx.Value(traceKey) != nil
}

// Add records x in ctx's scope chain and registry; without a scope it goes
// to the registry under Untagged; without either it is dropped.
func Add(ctx context.Context, x Sample) { add(ctx, x, false) }

// Spend adds points to Key{res, op} without counting a call: the cost a
// response reports for a request its transport already counted.
func Spend(ctx context.Context, res Resource, op string, points int64) {
	add(ctx, Sample{Res: res, Op: op, Points: points}, true)
}

func add(ctx context.Context, x Sample, spend bool) {
	s, ok := ctx.Value(scopeKey).(*Scope)
	if !ok {
		if r, ok := ctx.Value(registryKey).(*Registry); ok {
			r.entry(Untagged).record(x, spend)
		}
		return
	}
	if !s.traced {
		x.Detail = ""
	}
	if s.entry != nil {
		s.entry.record(x, spend)
	} else if r, ok := ctx.Value(registryKey).(*Registry); ok {
		r.entry(s.name).record(x, spend)
	}
	for ; s != nil; s = s.parent {
		s.record(x, spend)
	}
}

// Job is the scope name of a daemon job.
func Job(name string) string { return "job:" + name }

// Route is the scope name of an HTTP route: the ServeMux pattern, never the
// URL path.
func Route(pattern string) string { return "route:" + pattern }

// Loop is the scope name of a long-running loop.
func Loop(name string) string { return "loop:" + name }

// Untagged names the registry's totals of samples added outside any scope.
const Untagged = "untagged"
