package extract

import (
	"context"
	"time"

	"github.com/wloophq/wloop/plugins/slack/config"
)

// Next is when the last run said the job next has work: the earliest pending target's quiet time, max wait or
// retry, never before the breaker's trial or a spent daily cap frees; zero when nothing waits.
func (j *Job) Next() time.Time {
	j.wantMu.Lock()
	defer j.wantMu.Unlock()
	return j.next
}

func (j *Job) setNext(t time.Time) {
	j.wantMu.Lock()
	defer j.wantMu.Unlock()
	j.next = t
}

// nextRun is Next as the pending targets stand at now; j.mu is held.
func (j *Job) nextRun(ctx context.Context, ec config.SlackExtraction, now time.Time) time.Time {
	var at time.Time
	for k, p := range j.pending {
		t := p.newest.Add(ec.QuietFor())
		if waited := p.oldest.Add(ec.MaxWaitFor()); waited.Before(t) {
			t = waited
		}
		if j.wanted(k) {
			t = now
		}
		if t.Before(p.retryAt) {
			t = p.retryAt
		}
		if at.IsZero() || t.Before(at) {
			at = t
		}
	}
	if at.IsZero() {
		return at
	}
	if trial := j.brk.trial(); at.Before(trial) {
		at = trial
	}
	if why, err := j.capped(ctx, ec, now); err == nil && why != "" {
		if f := freesAt(now); at.Before(f) {
			at = f
		}
	}
	return at
}
