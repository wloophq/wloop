package extract

import (
	"context"
	"fmt"
	"testing"
	"time"
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
		r.cfg.Slack.Extraction.MaxDay = 1000
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
