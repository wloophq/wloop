package extract

import (
	"context"
	"fmt"
	"testing"
	"time"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// BenchmarkScanQuietPass is a pass over 5,000 new changes in 500 units, none due yet.
func BenchmarkScanQuietPass(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		r := newRig(b)
		for i := range 5000 {
			u := i % 500
			r.facts.post(jChan, ts(1+u+500*(i/500)), ts(1+u), jOther, fmt.Sprintf("m%d", i), jT0)
		}
		r.clock.set(jT0.Add(time.Second))
		b.StartTimer()
		if _, _, err := r.job.Run(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExtractPass extracts 300 due thread units with a scripted asker.
func BenchmarkExtractPass(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		r := newRig(b)
		r.cfg.Slack.Extraction.PerDayUSD = 1000
		postUnits(r, 300)
		r.clock.set(jT0.Add(time.Hour))
		b.StartTimer()
		if _, _, err := r.job.Run(context.Background()); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if n := r.asker.calls(); n != 300 {
			b.Fatalf("%d calls", n)
		}
		r.clock.set(jT0.Add(2 * time.Hour))
		start := time.Now()
		if _, _, err := r.job.Run(context.Background()); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(start).Microseconds()), "idle-pass-us")
		b.StartTimer()
	}
}

// BenchmarkCited reads the cited units of a 200-message window that links 200 threads of a
// ledger of 2,000 units and 20,000 items.
func BenchmarkCited(b *testing.B) {
	r := newRig(b)
	r.facts.convs[jOtherChan] = slack.Conversation{ID: jOtherChan, Kind: slack.KindChannel}
	var ops []items.Op
	for u := range 2000 {
		cu := slack.ThreadUnit(jTeam, jOtherChan, ts(u+1))
		for i := range 10 {
			ops = append(ops, items.Op{Op: items.OpCreate, Unit: cu, Item: fmt.Sprintf("e%d-%d", u, i),
				Anchor: unit.Anchor{Unit: cu, Ref: jOtherChan + "/" + ts(u+1)}, Fields: &items.Fields{Kind: ptr(items.Todo), Summary: ptr("work")}})
		}
	}
	if err := r.ledger.host.Apply(items.Extracted, ops...); err != nil {
		b.Fatal(err)
	}
	in := Input{Unit: thread(ts(90000))}
	for i := range 200 {
		in.Messages = append(in.Messages, slack.Message{TS: ts(90000 + i), Links: []string{permalinkOf(jOtherChan, ts(i*10+1)), "https://example.test/doc"}})
	}
	for b.Loop() {
		got, err := r.job.cited(context.Background(), jTeam, in, []unit.Unit{in.Unit})
		if err != nil || len(got) != maxCitedItems {
			b.Fatalf("%d cited, %v", len(got), err)
		}
	}
}
