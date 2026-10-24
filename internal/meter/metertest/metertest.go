// Package metertest asserts what a unit of work cost, in the manner of
// assertNumQueries: a failure lists the samples so an N+1 shows.
package metertest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/meter"
)

// Start opens a tracing scope named t.Name() under ctx and ends it at
// t.Cleanup. Every metered adapter called with the returned context, and
// every scope opened under it (a route, a job), adds to it.
func Start(t testing.TB, ctx context.Context) (context.Context, *meter.Scope) {
	ctx, sc := meter.Start(meter.Trace(ctx), t.Name())
	t.Cleanup(func() { sc.End() })
	return ctx, sc
}

// Field is the number of a Stat a Limit selects.
type Field uint8

const (
	Calls Field = iota // zero value: a Limit counts calls unless it says otherwise
	Spawns
	Rows
	Points
	Errors
	NotModified
	Bytes
)

func (f Field) String() string {
	switch f {
	case Calls:
		return "calls"
	case Spawns:
		return "spawns"
	case Rows:
		return "rows"
	case Points:
		return "points"
	case Errors:
		return "errors"
	case NotModified:
		return "not modified"
	case Bytes:
		return "bytes"
	}
	return fmt.Sprintf("field %d", uint8(f))
}

func (f Field) of(s meter.Stat) int64 {
	switch f {
	case Spawns:
		return s.Spawns
	case Rows:
		return s.Rows
	case Points:
		return s.Points
	case Errors:
		return s.Errors
	case NotModified:
		return s.NotModified
	case Bytes:
		return s.Bytes
	}
	return s.Calls
}

// Limit selects one number of a tally: Field of resource Res, of operation
// Op or of every operation when Op is "".
type Limit struct {
	Res   meter.Resource
	Op    string
	Field Field
}

func (l Limit) String() string {
	s := string(l.Res)
	if l.Op != "" {
		s += " " + l.Op
	}
	return s + " " + l.Field.String()
}

func (l Limit) of(t meter.Tally) int64 {
	if l.Op == "" {
		return l.Field.of(t.Total(l.Res))
	}
	return l.Field.of(t[meter.Key{Res: l.Res, Op: l.Op}])
}

// Budget is the most each Limit may reach; a Limit absent is unchecked.
type Budget map[Limit]int64

// Count is the number l selects in s's tally.
func Count(s *meter.Scope, l Limit) int64 { return l.of(s.Tally()) }

// Within fails t once per Limit over budget, naming the limit, the count
// and the budget, then, for each resource over budget, the scope's samples
// grouped by Detail with their count, most repeated first.
func Within(t testing.TB, s *meter.Scope, b Budget) {
	t.Helper()
	got := s.Tally()
	samples := s.Samples()
	for _, res := range over(t, got, b) {
		if samples == nil {
			t.Logf("%s ops of %s:\n%s", res, s.Name(), Report(only(got, res)))
			continue
		}
		t.Logf("%s samples of %s:\n%s", res, s.Name(), repeats(samples, res))
	}
}

// WithinTally is Within for a tally kept without its samples, such as a
// job's last run; it prints the tally's ops instead of samples.
func WithinTally(t testing.TB, got meter.Tally, b Budget) {
	t.Helper()
	for _, res := range over(t, got, b) {
		t.Logf("%s ops:\n%s", res, Report(only(got, res)))
	}
}

func only(t meter.Tally, res meter.Resource) meter.Tally {
	out := meter.Tally{}
	for k, st := range t {
		if k.Res == res {
			out[k] = st
		}
	}
	return out
}

// Report is the tally by resource and op, one per line, for t.Log in a
// baseline test.
func Report(t meter.Tally) string {
	var b strings.Builder
	for _, k := range t.Keys() {
		st := t[k]
		name := string(k.Res)
		if k.Op != "" {
			name += " " + k.Op
		}
		fmt.Fprintf(&b, "%s: %d calls", name, st.Calls)
		for _, f := range []Field{Spawns, Rows, Points, Errors, NotModified, Bytes} {
			if n := f.of(st); n > 0 {
				fmt.Fprintf(&b, ", %d %s", n, f)
			}
		}
		if st.Dur > 0 {
			fmt.Fprintf(&b, ", %v (max %v)", st.Dur, st.Max)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func over(t testing.TB, got meter.Tally, b Budget) []meter.Resource {
	t.Helper()
	limits := make([]Limit, 0, len(b))
	for l := range b {
		limits = append(limits, l)
	}
	sort.Slice(limits, func(i, j int) bool { return limits[i].String() < limits[j].String() })
	var out []meter.Resource
	for _, l := range limits {
		if n := l.of(got); n > b[l] {
			t.Errorf("%s = %d, budget %d", l, n, b[l])
			if !slices.Contains(out, l.Res) {
				out = append(out, l.Res)
			}
		}
	}
	return out
}

func repeats(samples []meter.Sample, res meter.Resource) string {
	type group struct {
		line string
		n    int
	}
	var groups []*group
	index := map[string]*group{}
	for _, x := range samples {
		if x.Res != res {
			continue
		}
		line := x.Op
		if x.Detail != "" {
			line += ": " + x.Detail
		}
		g, ok := index[line]
		if !ok {
			g = &group{line: line}
			index[line] = g
			groups = append(groups, g)
		}
		g.n++
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].n > groups[j].n })
	var b strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&b, "  ×%d %s\n", g.n, g.line)
	}
	return b.String()
}
