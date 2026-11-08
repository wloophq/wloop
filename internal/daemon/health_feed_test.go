package daemon

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/port"
)

// healthMessages counts the health messages that reach msgs within the health feed's quiet period.
func healthMessages(t *testing.T, msgs <-chan message) int {
	t.Helper()
	n := 0
	for _, m := range collectFor(msgs, healthQuiet+time.Second) {
		if m.c.Health {
			n++
		}
	}
	return n
}

// endsOf registers a job named name whose runs fail while fail holds, and returns a run of it.
func endsOf(s *Server, name string, fail *bool) func() {
	j := newJob(name, time.Hour, time.Minute, func(context.Context) (string, string, error) {
		if *fail {
			return "", "", errors.New("the source refused")
		}
		return "", "", nil
	})
	j.onEnd = s.jobEnded
	s.jobs[name] = j
	return func() { j.once(context.Background(), func(string, ...any) {}, s.jobDone) }
}

func TestAnIdleEndOfAJobNobodySeesPublishesNoHealth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := feedServer(t)
		fail := false
		run := endsOf(s, "extract:thread", &fail)
		run()
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		for range 3 {
			run()
			if n := healthMessages(t, msgs); n != 0 {
				t.Fatalf("an idle end of a job whose last run and last success nobody reads published %d health messages", n)
			}
		}
	})
}

func TestAnIngestEndPublishesHealth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := feedServer(t)
		fail := false
		run := endsOf(s, "ingest:thread", &fail)
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		time.Sleep(time.Second)
		run()
		if n := healthMessages(t, msgs); n != 1 {
			t.Fatalf("an ingestion end, whose last success the sync labels read, published %d health messages, want 1", n)
		}
	})
}

func TestAJobErrorPublishesHealthWhenItAppearsAndWhenItClears(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := feedServer(t)
		fail := false
		run := endsOf(s, "extract:thread", &fail)
		run()
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		fail = true
		run()
		if n := healthMessages(t, msgs); n != 1 {
			t.Fatalf("an error appearing published %d health messages, want 1", n)
		}
		run()
		if n := healthMessages(t, msgs); n != 0 {
			t.Fatalf("the same error again published %d health messages, want none", n)
		}
		fail = false
		run()
		if n := healthMessages(t, msgs); n != 1 {
			t.Fatalf("an error clearing published %d health messages, want 1", n)
		}
	})
}

func TestTheMenuBarConnectingPublishesHealth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := feedServer(t)
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		listen(t, s, func(r *http.Request) { r.URL.RawQuery += "&client=" + notifierClient })
		if n := healthMessages(t, msgs); n != 1 {
			t.Fatalf("the menu bar connecting, which the notifier field reads, published %d health messages, want 1", n)
		}
	})
}

// queueHealth is a brief queue whose counts and pause the test moves.
type queueHealth struct {
	pending atomic.Int64
	paused  atomic.Bool
}

func (b *queueHealth) Status(context.Context) (brief.Status, error) {
	st := brief.Status{Pending: int(b.pending.Load())}
	if b.paused.Load() {
		st.PausedUntil = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	}
	return st, nil
}

func (b *queueHealth) Breaker() (bool, time.Time, string, time.Time) {
	return false, time.Time{}, "", time.Time{}
}

func TestTheBriefCountsNoClientShowsPublishNoHealth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, log, _ := feedServer(t)
		b := &queueHealth{}
		s.deps.BriefStatus = b
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		b.pending.Store(3)
		log.Note(port.TopicBriefs)
		if n := healthMessages(t, msgs); n != 0 {
			t.Fatalf("a brief queued, which only doctor counts, published %d health messages", n)
		}
		b.paused.Store(true)
		log.Note(port.TopicBriefs)
		if n := healthMessages(t, msgs); n != 1 {
			t.Fatalf("the briefs pausing, which the dashboard shows, published %d health messages, want 1", n)
		}
	})
}

func TestTheViewerArrivingPublishesHealth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := feedServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); s.feedHealth(ctx) }()
		t.Cleanup(func() { cancel(); <-done })
		synctest.Wait()
		msgs := listen(t, s, nil)
		if _, err := s.viewerInfo(ctx); err != nil {
			t.Fatal(err)
		}
		if n := healthMessages(t, msgs); n != 1 {
			t.Fatalf("the viewer, which the health names, arriving published %d health messages, want 1", n)
		}
	})
}

func TestAHealthFieldNoNoteMovesIsPublishedByTheRecheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := feedServer(t)
		var warn atomic.Bool
		s.deps.EffectWarnings = func() []string {
			if warn.Load() {
				return []string{"an effect failed"}
			}
			return nil
		}
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		warn.Store(true)
		got := collectFor(msgs, healthRecheck+time.Second)
		n := 0
		for _, m := range got {
			if m.c.Health {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("a warning no note announces published %d health messages within a recheck, want 1", n)
		}
	})
}
