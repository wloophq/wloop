package slack

import (
	"context"
	"testing"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

func BenchmarkHeatSchedule500(b *testing.B) {
	f := newFixture(b)
	f.login(b)
	facts := scaleFacts(500, 1)
	f.seed(b, facts)
	objects := make([]string, len(facts.Threads))
	for i, t := range facts.Threads {
		objects[i] = dslack.ThreadObject(t.Conversation, t.TS)
	}
	ctx := context.Background()
	heat := f.p.src.store.Scheduler
	for _, c := range []struct {
		name  string
		sched dslack.Scheduler
	}{{"heat", heat}, {"every", Every(DefaultPoll)}} {
		b.Run(c.name, func(b *testing.B) {
			f.p.src.store.Scheduler = c.sched
			for b.Loop() {
				if err := f.p.src.store.See(ctx, objects, f.now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
