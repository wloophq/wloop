package brief

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
)

// Resync is how long Start sleeps when no deadline waits: a change nothing
// announced is found within it.
const Resync = 10 * time.Minute

// scanRetry is how long Start waits after a scan that could not read.
const scanRetry = time.Minute

// storeFault is err of a run that ended without being recorded: scan
// pauses for scanRetry, so a store that cannot write is not retried in a
// loop.
func (q *Queue) storeFault(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		q.mu.Lock()
		q.faultUntil = q.d.Now().Add(scanRetry)
		q.mu.Unlock()
	}
	return err
}

// minWait keeps a deadline already past from spinning Start.
const minWait = time.Second

// Touched wakes the scheduler for pull requests whose inputs changed; any
// number of calls before it scans make one scan.
func (q *Queue) Touched(refs []pr.Ref) {
	if len(refs) > 0 {
		q.wakeUp()
	}
}

// Due is signaled when followed pull requests are due a background brief.
func (q *Queue) Due() <-chan struct{} { return q.dueCh }

func (q *Queue) wakeUp() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) signalDue() {
	select {
	case q.dueCh <- struct{}{}:
	default:
	}
}

func (q *Queue) after(d time.Duration) <-chan time.Time {
	if q.d.After != nil {
		return q.d.After(d)
	}
	return time.After(d)
}

// Next is the earliest time the queue has something to do with states: the
// breaker's trial while it is open, else the retry of a wanted brief and
// the end of a failed background brief's backoff and the end of an edited
// description's quiet period. ok is false when nothing
// waits.
func (q *Queue) Next(states []inventory.BriefState, now time.Time) (next time.Time, ok bool) {
	if open, _, _, at := q.Breaker(); open {
		if q.trialRunning() {
			return time.Time{}, false
		}
		if at.After(now) {
			return at, true
		}
	}
	earliest := func(t time.Time) {
		if !ok || t.Before(next) {
			next, ok = t, true
		}
	}
	for _, st := range states {
		p, rec := st.PR, st.Rec
		if !st.Has || q.inFlight(p.Ref) {
			continue
		}
		_, v := q.version(p.Ref)
		switch {
		case !rec.WantedAt.IsZero():
			if (rec.Run == 0 || rec.Version != v) && (rec.AttemptOf != attemptOf(p, v) || rec.GaveUpAt.IsZero()) {
				earliest(rec.RetryAt)
			}
		case p.Followed() && tried(rec, true) && rec.Error != "" && rec.AttemptOf == attemptOf(p, v):
			if at := rec.AttemptedAt.Add(backoff(q.d.Retry, rec.Failures)); at.After(now) {
				earliest(at)
			}
		case p.Followed() && tried(rec, true) && rec.Error == "" && bodyEdited(p, rec) && !filesMoved(p, rec):
			if at := p.BodyEditedAt.Add(BodyQuiet); at.After(now) {
				earliest(at)
			}
		}
	}
	return next, ok
}

func (q *Queue) trialRunning() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.breaker.trial
}
