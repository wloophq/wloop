package slack

import (
	"context"
	"fmt"
	"testing"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
)

// BenchmarkSlackRows is one full report's rows: 500 threads of 10 messages, each with two open items.
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
	p := f.p.rowsSource()
	var n int
	for b.Loop() {
		units, err := p.Listed(ctx)
		if err != nil {
			b.Fatal(err)
		}
		in, err := p.Load(ctx, units)
		if err != nil {
			b.Fatal(err)
		}
		n = 0
		for _, u := range units {
			if x, ok := in[u]; ok {
				slackrows.Derive(x)
				n++
			}
		}
	}
	if n != 500 {
		b.Fatalf("%d rows", n)
	}
	b.ReportMetric(float64(n), "rows")
}
