package cli

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/meter"
)

// meteredProposer is the default workflows' proposer that keeps, of every
// sweep, the statements it ran and the transactions it opened.
type meteredProposer struct {
	*workflowProposer
	sweeps []meter.Tally
	byStmt map[string]int
}

func (p *meteredProposer) pass(context.Context) error {
	return p.r.untilQuiet(func() error {
		ctx, s := meter.Start(meter.Trace(context.Background()), "sweep")
		_, _, err := p.eng.Sweep(ctx, &sync.Mutex{})
		p.sweeps = append(p.sweeps, s.End())
		for _, x := range s.Samples() {
			if x.Res == meter.SQL {
				p.byStmt[x.Detail]++
			}
		}
		return err
	})
}

// The store budget of the sweeps of the default workflows over the replay:
// a pass reads each table once for every unit, in one read transaction, so
// the median sweep, which runs nothing, stays flat as the units grow; the
// heaviest runs items, whose steps read and write on their own.
const (
	sweepStatementsMedian = 112
	sweepStatementsMax    = 800
	sweepReadTxsMedian    = 5
	sweepReadTxsMax       = 55
)

func TestASweepOverTheReplayStaysWithinItsStoreBudget(t *testing.T) {
	t.Parallel()
	r := newReplay(t)
	p := &meteredProposer{workflowProposer: newWorkflowProposer(r), byStmt: map[string]int{}}
	r.run(p)
	var stmts, reads []int64
	for _, s := range p.sweeps {
		stmts = append(stmts, s.Total(meter.SQL).Calls)
		reads = append(reads, s[meter.Key{Res: meter.Tx, Op: "read"}].Calls)
	}
	for _, c := range []struct {
		what        string
		got         []int64
		median, max int64
	}{
		{"statements", stmts, sweepStatementsMedian, sweepStatementsMax},
		{"read transactions", reads, sweepReadTxsMedian, sweepReadTxsMax},
	} {
		if m := median(c.got); m > c.median {
			t.Errorf("the median sweep made %d %s, over its budget of %d", m, c.what, c.median)
		}
		if m := slices.Max(c.got); m > c.max {
			t.Errorf("a sweep made %d %s, over its budget of %d", m, c.what, c.max)
		}
	}
	if t.Failed() {
		logStatements(t, p.byStmt)
	}
}

func median(xs []int64) int64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

// logStatements logs the statements the sweeps ran most, for a budget a
// change went over.
func logStatements(t *testing.T, byStmt map[string]int) {
	type row struct {
		stmt string
		n    int
	}
	var rows []row
	for s, n := range byStmt {
		rows = append(rows, row{s, n})
	}
	slices.SortFunc(rows, func(a, b row) int { return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.stmt, b.stmt)) })
	for _, r := range rows[:min(20, len(rows))] {
		t.Logf("%6d %.160s", r.n, r.stmt)
	}
}
