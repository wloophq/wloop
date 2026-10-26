package brief

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// OnDemand is a BriefSource whose briefs another job writes, such as the
// Slack extraction, which a first turn asks for its unit's brief.
type OnDemand interface {
	// Want asks for u's brief now; false when no brief of u is coming,
	// such as when nothing of it is left to read.
	Want(ctx context.Context, u unit.Unit) (bool, error)
	// Watch has the source call noted each time it writes a unit's brief.
	Watch(noted func(u unit.Unit))
}

// Register adds p's BriefSource, watching an OnDemand one, before Start.
func (q *Queue) Register(p unit.ProviderID, s BriefSource) {
	q.mu.Lock()
	if q.d.Sources == nil {
		q.d.Sources = map[unit.ProviderID]BriefSource{}
	}
	q.d.Sources[p] = s
	q.mu.Unlock()
	if od, ok := s.(OnDemand); ok {
		od.Watch(q.Noted)
	}
}

// Noted wakes the Changed of u once another job wrote its brief.
func (q *Queue) Noted(u unit.Unit) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.wakeWaiters(u)
}

// wakeWaiters closes every Changed of u; q.mu is held.
func (q *Queue) wakeWaiters(u unit.Unit) {
	for _, c := range q.waiters[u] {
		close(c)
	}
	delete(q.waiters, u)
}

func refOf(u unit.Unit) (github.Ref, bool) {
	if u.Provider != unit.GitHub {
		return github.Ref{}, false
	}
	ref, err := pr.ParseKey(u.Key)
	return ref, err == nil
}

// Ready answers for a turn of u. A pull request is briefed by the queue: a
// first turn waits for a brief at the current version, a later one takes the
// brief there is. need are the extra fields the turn's intent needs: a first
// turn also waits until a call fills the missing ones, once, and a later one
// starts that call. A unit of another provider takes its brief in use; a
// first turn without one asks its OnDemand source and waits, at most
// WantedTimeout, while the source says a brief is coming.
func (q *Queue) Ready(ctx context.Context, u unit.Unit, first bool, need []string) (Readiness, error) {
	if ref, ok := refOf(u); ok {
		rd, err := q.readyPR(ctx, ref, first)
		if err != nil || rd.State != Ready || rd.Brief == nil {
			return rd, err
		}
		return q.needed(ctx, ref, rd, first, need, true), nil
	}
	return q.onDemand(ctx, u, first, true)
}

// Peek is Ready's answer without starting a brief, waiting or writing.
func (q *Queue) Peek(ctx context.Context, u unit.Unit, first bool, need []string) (Readiness, error) {
	if ref, ok := refOf(u); ok {
		rd, err := q.peekPR(ctx, ref, first)
		if err != nil || rd.State != Ready || rd.Brief == nil {
			return rd, err
		}
		return q.needed(ctx, ref, rd, first, need, false), nil
	}
	return q.onDemand(ctx, u, first, false)
}

// needed is rd, a ready brief of ref, waiting for a first turn while a
// call fills the needed fields it lacks; a live call starts it. A fill that
// failed at this version lets the turn go with the brief as it is.
func (q *Queue) needed(ctx context.Context, ref github.Ref, rd Readiness, first bool, need []string, live bool) Readiness {
	bi := q.intent()
	missing := slices.DeleteFunc(bi.Schema.Missing(*rd.Brief), func(n string) bool { return !slices.Contains(need, n) })
	if len(missing) == 0 {
		return rd
	}
	_, version := q.version(ref)
	q.mu.Lock()
	failed := q.fillFailed[ref] == version
	q.mu.Unlock()
	if failed {
		return rd
	}
	if live && !q.inFlight(ref) {
		base := context.WithoutCancel(ctx)
		q.wg.Go(func() {
			if _, err := q.fillPR(base, ref, missing); err != nil {
				q.d.Log("brief %s: %v", ref, err)
				q.mu.Lock()
				if q.fillFailed == nil {
					q.fillFailed = map[github.Ref]string{}
				}
				q.fillFailed[ref] = version
				q.mu.Unlock()
				q.Noted(ref.Unit())
			}
		})
	}
	if !first {
		return rd
	}
	return Readiness{State: Wait, Wait: WaitBriefing, Attempt: 1}
}

// onDemand answers for a unit whose briefs another job writes.
func (q *Queue) onDemand(ctx context.Context, u unit.Unit, first, live bool) (Readiness, error) {
	if !q.d.Enabled {
		rd := without(ReasonOff)
		rd.Cause = intake.CauseOff
		return rd, nil
	}
	ub, ok, err := q.d.Store.UnitBrief(ctx, u)
	if err != nil {
		return Readiness{}, err
	}
	if ok {
		if b, err := Parse(ub.Brief); err == nil && b != nil {
			q.mu.Lock()
			delete(q.demand, u)
			q.mu.Unlock()
			return Readiness{State: Ready, Brief: b, Run: ub.Run}, nil
		}
	}
	if !first {
		return without(ReasonNone), nil
	}
	q.mu.Lock()
	src := q.d.Sources[u.Provider]
	since, asked := q.demand[u]
	q.mu.Unlock()
	od, ok := src.(OnDemand)
	if !ok {
		return without(fmt.Sprintf("%s units have no brief source", u.Provider)), nil
	}
	now := q.d.Now()
	deadline := since.Add(cmp.Or(q.d.WantedTimeout, WantedTimeout))
	if asked && !now.Before(deadline) {
		return without("no brief came in time"), nil
	}
	coming := asked
	if live {
		if coming, err = od.Want(ctx, u); err != nil {
			return Readiness{}, err
		}
		if coming && !asked {
			q.mu.Lock()
			if q.demand == nil {
				q.demand = map[unit.Unit]time.Time{}
			}
			q.demand[u], since = now, now
			q.mu.Unlock()
			deadline = now.Add(cmp.Or(q.d.WantedTimeout, WantedTimeout))
		}
	}
	if !coming {
		return without("nothing of it is waiting to be read"), nil
	}
	return Readiness{State: Wait, Wait: WaitBriefing, Attempt: 1, Next: deadline}, nil
}

// Changed is closed by the next end of a brief run of u after the call, or
// by another job noting u's brief; the func releases it.
func (q *Queue) Changed(u unit.Unit) (<-chan struct{}, func()) {
	c := make(chan struct{})
	q.mu.Lock()
	if q.waiters == nil {
		q.waiters = map[unit.Unit][]chan struct{}{}
	}
	q.waiters[u] = append(q.waiters[u], c)
	q.mu.Unlock()
	return c, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		ws := q.waiters[u]
		for i, w := range ws {
			if w == c {
				q.waiters[u] = append(ws[:i:i], ws[i+1:]...)
				break
			}
		}
		if len(q.waiters[u]) == 0 {
			delete(q.waiters, u)
		}
	}
}

// Unwant drops u's want of a brief, once no turn waits on it.
func (q *Queue) Unwant(ctx context.Context, u unit.Unit) error {
	if ref, ok := refOf(u); ok {
		return q.unwantPR(ctx, ref)
	}
	q.mu.Lock()
	delete(q.demand, u)
	q.mu.Unlock()
	return nil
}

// Fill fills the extra fields u's brief lacks, those of need when need is
// not empty, without briefing its core again; see fillPR. Only pull
// requests' briefs are filled this way: another provider's job writes its
// fields at its next pass.
func (q *Queue) Fill(ctx context.Context, u unit.Unit, need []string) (bool, error) {
	if ref, ok := refOf(u); ok {
		return q.fillPR(ctx, ref, need)
	}
	return false, nil
}
