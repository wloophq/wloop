package daemon

import (
	"context"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

// wakeBriefs records what the daemon hands the queue, and signals Due on
// due.
type wakeBriefs struct {
	stubBriefs
	mu      sync.Mutex
	touched [][]github.Ref
	due     chan struct{}
}

func (b *wakeBriefs) Touched(refs []github.Ref) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.touched = append(b.touched, refs)
}

func (b *wakeBriefs) Due() <-chan struct{} { return b.due }

func (b *wakeBriefs) touches() [][]github.Ref {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.touched)
}

func briefWakeServer(t *testing.T) (*Server, *change.Log, *wakeBriefs) {
	t.Helper()
	log := newChangeLog()
	b := &wakeBriefs{due: make(chan struct{}, 1)}
	s := newServerWith(t, func(d *Deps) {
		d.StateDir = t.TempDir()
		d.Changes = log
		withBriefs(d, b)
		d.Collector = failingCollector(t, *d)
	})
	t.Cleanup(s.wakeBrief())
	return s, log, b
}

// waitsFor says whether j would run within d with nothing else happening.
func waitsFor(j *job, d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return j.wait(ctx)
}

func TestNoBriefInputNoteRunsNoBriefJob(t *testing.T) {
	s, log, b := briefWakeServer(t)
	j := s.jobs["brief"]
	j.once(context.Background(), t.Logf, func(string, string) {})
	s.jobs["fast"].once(context.Background(), t.Logf, func(string, string) {})
	log.Note(port.TopicLoopEvents|port.TopicBriefs, r7.Unit())
	log.Note(port.TopicJobs)
	log.Note(port.TopicPRs, unit.Unit{Provider: unit.Session, Key: "term_1"})
	if got := b.touches(); len(got) != 0 {
		t.Fatalf("touched %v", got)
	}
	if waitsFor(j, 50*time.Millisecond) {
		t.Fatal("the brief job ran after a fast run that changed no brief input")
	}
}

func TestFastWithADirtyRefWakesTheQueueOnce(t *testing.T) {
	s, log, b := briefWakeServer(t)
	j := s.jobs["brief"]
	j.once(context.Background(), t.Logf, func(string, string) {})
	log.Note(port.TopicPRs|port.TopicSourceEvents, r7.Unit(), unit.Unit{Provider: unit.Session, Key: "term_1"})
	if got := b.touches(); len(got) != 1 || !slices.Equal(got[0], []github.Ref{r7}) {
		t.Fatalf("touched %v, want [[%v]]", got, r7)
	}
	if waitsFor(j, 50*time.Millisecond) {
		t.Fatal("a touch ran the brief job before the queue found anything due")
	}
	b.due <- struct{}{}
	if !waitsFor(j, time.Second) {
		t.Fatal("Due did not run the brief job")
	}
	if waitsFor(j, 50*time.Millisecond) {
		t.Fatal("one Due ran the brief job twice")
	}
}

func TestHealthAtMostThreeQueries(t *testing.T) {
	f := storetest.Seeded(t, storetest.Live)
	q := brief.New(brief.Deps{Store: f.Inventory, Taxonomy: func(github.Ref) string { return brief.DefaultTaxonomy },
		Retry: time.Hour, Changes: f.Log, Now: func() time.Time { return f.Now }, Log: t.Logf})
	s := newServerWith(t, func(d *Deps) {
		*d = withStore(*d, f.Inventory)
		d.Pauses, d.Changes = f.Rounds, f.Log
		withBriefs(d, q)
		d.Now = func() time.Time { return f.Now }
	})
	for i, want := range []int64{3, 2, 2} {
		ctx, sc := metertest.Start(t, context.Background())
		rec := httptest.NewRecorder()
		s.handleHealth(rec, httptest.NewRequest("GET", "/api/health", nil).WithContext(ctx))
		if rec.Code != 200 {
			t.Fatalf("call %d: %d %s", i, rec.Code, rec.Body)
		}
		metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: want})
	}
}
