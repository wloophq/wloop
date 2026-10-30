package brief

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/event"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

const (
	// BreakerAfter consecutive global failures, across pull requests, open
	// the breaker.
	BreakerAfter = 3
	// BreakerCool is how long the breaker stays open before a trial.
	BreakerCool = 15 * time.Minute
)

// Events of the breaker, on no pull request, keyed by the time: Data is
// the error that opened it, the trial run that closed it, or the error of
// a trial that kept it open.
const (
	KindBreakerOpened      = "brief_breaker_opened"
	KindBreakerClosed      = "brief_breaker_closed"
	KindBreakerTrialFailed = "brief_breaker_trial_failed"
)

// breaker stops every brief while the agent or gh is out. It lives in
// memory: a daemon starts closed, so its first call is the trial.
type breaker struct {
	count       int
	open, trial bool
	since, next time.Time
	lastErr     string
}

type breakerChange int

const (
	breakerKept breakerChange = iota
	breakerOpened
	breakerClosed
	breakerTrialFailed
)

// Breaker is the state the health and the menu bar show; open is false
// while it is closed.
func (q *Queue) Breaker() (open bool, since time.Time, lastErr string, next time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := q.breaker
	return b.open, b.since, b.lastErr, b.next
}

func (q *Queue) isOpen() bool {
	open, _, _, _ := q.Breaker()
	return open
}

func (q *Queue) succeeded() breakerChange {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recent, q.globalTries = nil, nil
	wasOpen := q.breaker.open
	q.breaker = breaker{}
	if wasOpen {
		return breakerClosed
	}
	return breakerKept
}

// noteGlobal keeps err as the last global error, logged when it is new.
func (q *Queue) noteGlobal(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err.Error() != q.breaker.lastErr {
		q.d.Log("brief: %v", err)
	}
	q.breaker.lastErr = err.Error()
}

// globalFailure counts a global failure toward the closed breaker and opens
// it on the BreakerAfter-th in a row, or at once when now is set.
func (q *Queue) globalFailure(at time.Time, now bool) breakerChange {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := &q.breaker
	if b.open {
		return breakerKept
	}
	b.count++
	if !now && b.count < BreakerAfter {
		return breakerKept
	}
	b.open, b.since, b.next = true, at, at.Add(BreakerCool)
	q.d.Log("brief: briefs are unavailable; "+brand.Name+" tries again at %s", b.next.Local().Format("15:04"))
	return breakerOpened
}

// trialFailed keeps the breaker open for another BreakerCool: only a brief
// that works closes it.
func (q *Queue) trialFailed(at time.Time) breakerChange {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.breaker.open {
		return breakerKept
	}
	q.breaker.next = at.Add(BreakerCool)
	return breakerTrialFailed
}

func (q *Queue) recordBreaker(ctx context.Context, change breakerChange, run dinv.BriefRun) {
	ev := dinv.Event{At: run.EndedAt, Key: run.EndedAt.UTC().Format(event.TimeLayout), Data: run.Error}
	switch change {
	case breakerOpened:
		ev.Kind = KindBreakerOpened
	case breakerClosed:
		ev.Kind, ev.Data = KindBreakerClosed, strconv.FormatInt(run.ID, 10)
	case breakerTrialFailed:
		ev.Kind = KindBreakerTrialFailed
	default:
		return
	}
	if err := q.d.Store.RecordEvent(ctx, ev); err != nil {
		q.d.Log("brief: recording %s: %v", ev.Kind, err)
	}
}

// startTrial claims ref for the breaker's trial: false unless the breaker
// is open, its cool is over, no trial runs and ref is not in flight.
func (q *Queue) startTrial(ref github.Ref, now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := &q.breaker
	if !b.open || b.trial || now.Before(b.next) || q.inflight[ref] {
		return false
	}
	if q.inflight == nil {
		q.inflight = map[github.Ref]bool{}
	}
	q.inflight[ref], b.trial = true, true
	return true
}

// trial runs the trial whose claim startTrial took.
func (q *Queue) trial(ctx context.Context, p dinv.PR, timeout time.Duration) {
	defer q.endTrial()
	_ = q.brief(ctx, p, TriggerTrial, timeout)
}

func (q *Queue) endTrial() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.breaker.trial = false
	q.wakeUp()
}

// Start runs wanted briefs and the breaker's trials until ctx ends, waking
// at the earliest deadline, on Touched and on the end of a run, and after
// Resync at most.
func (q *Queue) Start(ctx context.Context) {
	q.mu.Lock()
	q.base = ctx
	q.mu.Unlock()
	for {
		select {
		case <-q.wake:
		default:
		}
		wait := Resync
		next, ok, err := q.scan(ctx)
		if err != nil {
			wait = scanRetry
			if ctx.Err() == nil {
				q.d.Log("brief: %v", err)
			}
		} else if ok {
			wait = min(max(next.Sub(q.d.Now()), minWait), Resync)
		}
		select {
		case <-ctx.Done():
			q.wg.Wait()
			return
		case <-q.wake:
		case <-q.after(wait):
		}
	}
}

// scan runs the trial once the breaker's cool is over, or, while it is
// closed, the wanted briefs whose retry is due and no one waits on, and
// says when it next has something to do. It tells the brief job when
// followed pull requests are due a background brief.
func (q *Queue) scan(ctx context.Context) (next time.Time, ok bool, err error) {
	now := q.d.Now()
	q.mu.Lock()
	open, trial, at, fault := q.breaker.open, q.breaker.trial, q.breaker.next, q.faultUntil
	q.mu.Unlock()
	if now.Before(fault) {
		return fault, true, nil
	}
	if open && (trial || now.Before(at)) {
		return at, open && !trial, nil
	}
	states, err := q.d.Store.BriefStates(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	wanted := q.wanted(states, now)
	if open {
		q.runTrial(ctx, states, wanted, now)
	} else {
		for _, st := range wanted {
			if q.claimIf(ctx, st.PR.Ref, func(rec dinv.BriefRecord, has bool, version string) bool {
				return q.wantedNow(st.PR, rec, has, version, q.d.Now())
			}) {
				q.spawn(ctx, st.PR, TriggerWanted, q.timeout(true), true)
			}
		}
		if len(q.due(states, now)) > 0 {
			q.signalDue()
		}
	}
	next, ok = q.Next(states, now)
	return next, ok, nil
}

// wantedNow is a pull request a first turn wants a brief of, with no brief
// at the current version, not given up and not waiting for a retry.
func (q *Queue) wantedNow(p dinv.PR, rec dinv.BriefRecord, has bool, version string, now time.Time) bool {
	return has && !rec.WantedAt.IsZero() && (rec.Run == 0 || rec.Version != version) &&
		(rec.AttemptOf != attemptOf(p, version) || rec.GaveUpAt.IsZero() && !now.Before(rec.RetryAt))
}

// wanted lists the wanted pull requests not in flight, the oldest want
// first.
func (q *Queue) wanted(states []inventory.BriefState, now time.Time) []inventory.BriefState {
	var out []inventory.BriefState
	for _, st := range states {
		if _, v := q.version(st.PR.Ref); q.inFlight(st.PR.Ref) || !q.wantedNow(st.PR, st.Rec, st.Has, v, now) {
			continue
		}
		out = append(out, st)
	}
	slices.SortStableFunc(out, func(a, b inventory.BriefState) int { return a.Rec.WantedAt.Compare(b.Rec.WantedAt) })
	return out
}

// runTrial briefs the first candidate it can claim: the oldest wanted pull
// request, else the followed one that needs a brief and moved least
// recently, backoff aside.
func (q *Queue) runTrial(ctx context.Context, states, wanted []inventory.BriefState, now time.Time) {
	for _, st := range wanted {
		if q.startTrial(st.PR.Ref, now) {
			q.spawn(ctx, st.PR, TriggerTrial, q.timeout(true), true)
			return
		}
	}
	var rest []dinv.PR
	for _, st := range states {
		if _, v := q.version(st.PR.Ref); st.PR.Followed() && st.Rec.WantedAt.IsZero() && q.isDue(st.PR, st.Rec, st.Has, v, now, false) {
			rest = append(rest, st.PR)
		}
	}
	slices.SortStableFunc(rest, func(a, b dinv.PR) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	for _, p := range rest {
		if q.startTrial(p.Ref, now) {
			q.spawn(ctx, p, TriggerTrial, q.timeout(false), false)
			return
		}
	}
}
