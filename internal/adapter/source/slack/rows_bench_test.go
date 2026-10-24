package slack

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

// BenchmarkSlackRows is one rebuild's Slack part, in one read snapshot as the read model loads it: 500 threads of 10 messages, each with two open items.
func BenchmarkSlackRows(b *testing.B) {
	ctx := context.Background()
	f := newFixture(b)
	f.login(b)
	f.people(b)
	facts := scaleFacts(500, 10)
	for i := range facts.Conversations {
		facts.Conversations[i].Name = fmt.Sprintf("c%d", i)
	}
	f.seed(b, facts)
	l := f.ledger()
	var ops []items.Op
	for _, th := range facts.Threads {
		u := dslack.ThreadUnit(team, th.Conversation, th.TS)
		ops = append(ops, createOp(u, "e1", 1, mePerson, otherPerson), createOp(u, "e2", 2, otherPerson, mePerson))
	}
	applyOps(b, l, items.Extracted, ops...)
	p := f.p.rows(readmodel.RowDeps{})
	var n int
	for b.Loop() {
		var units []unit.Unit
		var in map[unit.Unit]any
		err := f.db.Snapshot(ctx, func(ctx context.Context) error {
			var err error
			if units, err = p.Listed(ctx); err != nil {
				return err
			}
			in, err = p.Load(ctx, units)
			return err
		})
		if err != nil {
			b.Fatal(err)
		}
		n = 0
		for _, u := range units {
			if x, ok := in[u]; ok {
				if _, err := p.Derive(readmodel.Base{}, x, time.Time{}); err != nil {
					b.Fatal(err)
				}
				n++
			}
		}
	}
	if n != 500 {
		b.Fatalf("%d rows", n)
	}
	b.ReportMetric(float64(n), "rows")
}
