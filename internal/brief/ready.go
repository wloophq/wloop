package brief

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/intake"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

const (
	// MaxFailures failures of a wanted brief that belong to its pull
	// request, on one attempt_of, give it up.
	MaxFailures   = 3
	WantedTimeout = 30 * time.Second
	// SessionWait is how long a turn asked for inside an agent session waits
	// for its brief, under the 2 minutes an agent's shell tool allows.
	SessionWait = 90 * time.Second
)

// WantedRetry is the wait before the second and the third attempt of a
// wanted brief.
var WantedRetry = []time.Duration{time.Minute, 4 * time.Minute}

type ReadyState string

const (
	Ready   ReadyState = "ready"
	Wait    ReadyState = "wait"
	Without ReadyState = "without"
)

// The waits of a Readiness.
const (
	WaitBriefing = intake.WaitBriefing
	WaitRetry    = intake.WaitRetry
)

const (
	ReasonOff  = "briefs are off"
	ReasonNone = "this pull request has no brief yet"
)

// Readiness answers whether a turn goes now. Ready carries Brief, made by
// Run. Wait names what the turn waits on: Attempt is the attempt it waits
// for, LastError the failure of the one before, Next the retry's time.
// Without says why in Reason, and Cause names a give-up (Attempt failures,
// the last LastError), an open breaker (since Since, for LastError) or
// briefs that are disabled.
type Readiness struct {
	State       ReadyState
	Wait        string
	Attempt     int
	LastError   string
	Next, Since time.Time
	Reason      string
	Cause       string
	Brief       *Brief
	Run         int64
}

func without(reason string) Readiness { return Readiness{State: Without, Reason: reason} }

// Ready answers for a turn of ref. A first turn waits for a brief at the
// current version, and its wanting one starts that brief at once; a later
// turn takes the brief there is, stale or not, starts a refresh when one is
// due, and never waits.
func (q *Queue) Ready(ctx context.Context, ref github.Ref, first bool) (Readiness, error) {
	if !q.d.Enabled {
		rd := without(ReasonOff)
		rd.Cause = intake.CauseOff
		return rd, nil
	}
	p, inInventory, err := q.d.Store.PR(ctx, ref)
	if err != nil {
		return Readiness{}, err
	}
	if !inInventory {
		p = dinv.PR{Ref: ref}
	}
	q.state.Lock()
	defer q.state.Unlock()
	rec, has, err := q.d.Store.BriefRecord(ctx, ref)
	if err != nil {
		return Readiness{}, err
	}
	_, version := q.version(ref)
	now := q.d.Now()
	b := stored(rec)
	if !first {
		if inInventory && has && refreshDue(p, rec, version, now, q.d.Retry) && !q.isOpen() && q.claim(ref) {
			q.spawn(ctx, p, TriggerRefresh, q.timeout(true), false)
		}
		if b == nil {
			return without(ReasonNone), nil
		}
		return Readiness{State: Ready, Brief: b, Run: rec.Run}, nil
	}
	cur := attemptOf(p, version)
	rd := Readiness{State: Wait, Wait: WaitBriefing, Attempt: q.attempt(ref, rec, cur)}
	if rd.Attempt > 1 {
		rd.LastError = rec.Error
	}
	switch open, since, lastErr, next := q.Breaker(); {
	case b != nil && rec.Version == version:
		return Readiness{State: Ready, Brief: b, Run: rec.Run}, q.clearWanted(ctx, ref, rec)
	case b != nil && b.validate() == nil:
		if inInventory && !open && q.claim(ref) {
			q.spawn(ctx, p, TriggerRefresh, q.timeout(true), false)
		}
		return Readiness{State: Ready, Brief: b, Run: rec.Run}, q.clearWanted(ctx, ref, rec)
	case rec.AttemptOf == cur && !rec.GaveUpAt.IsZero():
		gave := without(rec.GaveUpReason)
		gave.Cause, gave.Attempt, gave.LastError = intake.CauseGaveUp, rec.Failures, rec.Error
		return gave, q.clearWanted(ctx, ref, rec)
	case open && q.startTrial(ref, now):
		if err := q.want(ctx, ref, rec, now); err != nil {
			q.unclaim(ref)
			q.endTrial()
			return Readiness{}, err
		}
		q.spawn(ctx, p, TriggerTrial, q.timeout(true), true)
		return rd, nil
	case open && q.inFlight(ref):
		return rd, q.want(ctx, ref, rec, now)
	case open:
		reason := fmt.Sprintf("briefs are unavailable since %s: %s; "+brand.Name+" tries again at %s",
			since.Local().Format("15:04"), lastErr, next.Local().Format("15:04"))
		off := without(reason)
		off.Cause, off.Since, off.LastError = intake.CauseBreaker, since, lastErr
		return off, q.clearWanted(ctx, ref, rec)
	}
	if err := q.want(ctx, ref, rec, now); err != nil {
		return Readiness{}, err
	}
	switch {
	case q.inFlight(ref):
	case rec.AttemptOf == cur && now.Before(rec.RetryAt):
		rd.Wait, rd.Next = WaitRetry, rec.RetryAt
	case q.claim(ref):
		q.spawn(ctx, p, TriggerWanted, q.timeout(true), true)
	}
	return rd, nil
}

// Peek is Ready's answer without starting a brief, waiting or writing: a
// plan reads it to say whether a round would go now.
func (q *Queue) Peek(ctx context.Context, ref github.Ref, first bool) (Readiness, error) {
	if !q.d.Enabled {
		rd := without(ReasonOff)
		rd.Cause = intake.CauseOff
		return rd, nil
	}
	p, inInventory, err := q.d.Store.PR(ctx, ref)
	if err != nil {
		return Readiness{}, err
	}
	if !inInventory {
		p = dinv.PR{Ref: ref}
	}
	q.state.Lock()
	rec, _, err := q.d.Store.BriefRecord(ctx, ref)
	q.state.Unlock()
	if err != nil {
		return Readiness{}, err
	}
	_, version := q.version(ref)
	b := stored(rec)
	if !first {
		if b == nil {
			return without(ReasonNone), nil
		}
		return Readiness{State: Ready, Brief: b, Run: rec.Run}, nil
	}
	cur := attemptOf(p, version)
	rd := Readiness{State: Wait, Wait: WaitBriefing, Attempt: q.attempt(ref, rec, cur)}
	if rd.Attempt > 1 {
		rd.LastError = rec.Error
	}
	open, since, lastErr, next := q.Breaker()
	switch {
	case b != nil && (rec.Version == version || b.validate() == nil):
		return Readiness{State: Ready, Brief: b, Run: rec.Run}, nil
	case rec.AttemptOf == cur && !rec.GaveUpAt.IsZero():
		gave := without(rec.GaveUpReason)
		gave.Cause, gave.Attempt, gave.LastError = intake.CauseGaveUp, rec.Failures, rec.Error
		return gave, nil
	case open && (q.inFlight(ref) || !q.d.Now().Before(next)):
		return rd, nil
	case open:
		reason := fmt.Sprintf("briefs are unavailable since %s: %s; "+brand.Name+" tries again at %s",
			since.Local().Format("15:04"), lastErr, next.Local().Format("15:04"))
		off := without(reason)
		off.Cause, off.Since, off.LastError = intake.CauseBreaker, since, lastErr
		return off, nil
	case !q.inFlight(ref) && rec.AttemptOf == cur && q.d.Now().Before(rec.RetryAt):
		rd.Wait, rd.Next = WaitRetry, rec.RetryAt
	}
	return rd, nil
}

// attempt is the attempt a first turn of ref waits on: one past the
// failures on cur, its pull request's and the global ones.
func (q *Queue) attempt(ref github.Ref, rec dinv.BriefRecord, cur string) int {
	if rec.AttemptOf != cur {
		return 1
	}
	return 1 + rec.Failures + q.triesOf(ref, cur)
}

// Attempt is the attempt a first turn of ref waits on, as Ready counts it.
func (q *Queue) Attempt(ctx context.Context, ref github.Ref) (int, error) {
	p, ok, err := q.d.Store.PR(ctx, ref)
	if err != nil {
		return 0, err
	}
	if !ok {
		p = dinv.PR{Ref: ref}
	}
	q.state.Lock()
	rec, _, err := q.d.Store.BriefRecord(ctx, ref)
	q.state.Unlock()
	if err != nil {
		return 0, err
	}
	_, version := q.version(ref)
	return q.attempt(ref, rec, attemptOf(p, version)), nil
}

func (q *Queue) want(ctx context.Context, ref github.Ref, rec dinv.BriefRecord, now time.Time) error {
	if !rec.WantedAt.IsZero() {
		return nil
	}
	defer q.wakeUp()
	return q.d.Store.WantBrief(ctx, ref, now)
}

func stored(rec dinv.BriefRecord) *Brief {
	if rec.Run == 0 || rec.Brief == "" {
		return nil
	}
	var b Brief
	if json.Unmarshal([]byte(rec.Brief), &b) != nil {
		return nil
	}
	return &b
}

// clearWanted ends the want once a first turn goes, with the brief or
// without it.
func (q *Queue) clearWanted(ctx context.Context, ref github.Ref, rec dinv.BriefRecord) error {
	if rec.WantedAt.IsZero() {
		return nil
	}
	defer q.wakeUp()
	return q.d.Store.ClearWanted(ctx, ref)
}

// spawn runs a brief whose claim is held, WantedConcurrency at a time,
// outside the asker's context: a wanted run is never canceled when its
// waiter goes away, only when the queue's Start ends. A wanted run that
// nobody wants by the time it gets its turn does not run. A run a turn
// waits on reads GitHub at PriorityUser.
func (q *Queue) spawn(ctx context.Context, p dinv.PR, trigger string, timeout time.Duration, waiter bool) {
	q.mu.Lock()
	base := q.base
	q.mu.Unlock()
	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	base = dgh.WithPriority(base, priority(waiter))
	sem := q.wantedSem
	if trigger == TriggerRefresh {
		sem = q.refreshSem
	}
	q.wg.Go(func() {
		sem <- struct{}{}
		defer func() { <-sem }()
		if trigger == TriggerWanted && !q.stillWanted(base, p.Ref) {
			q.release(p.Ref)
			return
		}
		if trigger == TriggerTrial {
			q.trial(base, p, timeout)
			return
		}
		_ = q.brief(base, p, trigger, timeout)
	})
}

// stillWanted is false once a cancel or a start without the brief cleared
// ref's want; a store error reads as wanted.
func (q *Queue) stillWanted(ctx context.Context, ref github.Ref) bool {
	q.state.Lock()
	defer q.state.Unlock()
	rec, _, err := q.d.Store.BriefRecord(ctx, ref)
	return err != nil || !rec.WantedAt.IsZero()
}

// claimIf claims ref when, read again now, its record still passes ok: a
// snapshot may be stale by the time the claim frees.
func (q *Queue) claimIf(ctx context.Context, ref github.Ref, ok func(rec dinv.BriefRecord, has bool, version string) bool) bool {
	if !q.claim(ref) {
		return false
	}
	q.state.Lock()
	rec, has, err := q.d.Store.BriefRecord(ctx, ref)
	q.state.Unlock()
	_, version := q.version(ref)
	if err != nil || !ok(rec, has, version) {
		q.unclaim(ref)
		return false
	}
	return true
}

// unclaim drops a claim that ran nothing, waking no Changed.
func (q *Queue) unclaim(ref github.Ref) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inflight, ref)
}

// claim takes ref for one brief run; false when one already runs.
func (q *Queue) claim(ref github.Ref) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.inflight[ref] {
		return false
	}
	if q.inflight == nil {
		q.inflight = map[github.Ref]bool{}
	}
	q.inflight[ref] = true
	return true
}

// release ends ref's run and wakes every Changed of it.
func (q *Queue) release(ref github.Ref) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inflight, ref)
	for _, c := range q.waiters[ref] {
		close(c)
	}
	delete(q.waiters, ref)
	q.wakeUp()
}

func (q *Queue) inFlight(ref github.Ref) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.inflight[ref]
}

// Changed is closed by the next end of a brief run of ref after the call;
// the func releases it.
func (q *Queue) Changed(ref github.Ref) (<-chan struct{}, func()) {
	c := make(chan struct{})
	q.mu.Lock()
	if q.waiters == nil {
		q.waiters = map[github.Ref][]chan struct{}{}
	}
	q.waiters[ref] = append(q.waiters[ref], c)
	q.mu.Unlock()
	return c, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		ws := q.waiters[ref]
		for i, w := range ws {
			if w == c {
				q.waiters[ref] = append(ws[:i:i], ws[i+1:]...)
				break
			}
		}
		if len(q.waiters[ref]) == 0 {
			delete(q.waiters, ref)
		}
	}
}

// Intakes is the queue as the intake's Briefs port.
type Intakes struct{ Q *Queue }

var _ intake.Briefs = Intakes{}

func (a Intakes) Ready(ctx context.Context, u unit.Unit, first bool) (intake.Readiness, error) {
	ref, err := pr.ParseKey(u.Key)
	if err != nil || u.Provider != unit.GitHub {
		return intake.Readiness{}, fmt.Errorf("no brief for %s", u)
	}
	rd, err := a.Q.Ready(ctx, github.Ref(ref), first)
	if err != nil {
		return intake.Readiness{}, err
	}
	return readinessOf(rd), nil
}

func (a Intakes) Changed(u unit.Unit) (<-chan struct{}, func()) {
	ref, err := pr.ParseKey(u.Key)
	if err != nil || u.Provider != unit.GitHub {
		return make(chan struct{}), func() {}
	}
	return a.Q.Changed(github.Ref(ref))
}

func (a Intakes) Unwant(ctx context.Context, u unit.Unit) error {
	ref, err := pr.ParseKey(u.Key)
	if err != nil || u.Provider != unit.GitHub {
		return nil
	}
	return a.Q.Unwant(ctx, github.Ref(ref))
}

// Peek is Ready without starting a brief or writing.
func (a Intakes) Peek(ctx context.Context, u unit.Unit, first bool) (intake.Readiness, error) {
	ref, err := pr.ParseKey(u.Key)
	if err != nil || u.Provider != unit.GitHub {
		return intake.Readiness{}, fmt.Errorf("no brief for %s", u)
	}
	rd, err := a.Q.Peek(ctx, github.Ref(ref), first)
	if err != nil {
		return intake.Readiness{}, err
	}
	return readinessOf(rd), nil
}

func readinessOf(rd Readiness) intake.Readiness {
	return intake.Readiness{Go: rd.State != Wait, Without: rd.State == Without, Cause: rd.Cause,
		Wait: rd.Wait, Attempt: rd.Attempt, Note: rd.Reason, LastError: rd.LastError, Next: rd.Next, Since: rd.Since}
}
