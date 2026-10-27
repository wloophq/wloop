package brief

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

var unavailable = fmt.Errorf("%w: claude: API Error: 529 Overloaded", turns.ErrUnavailable)

func (r *rig) breakerEvents(t *testing.T, kind string) []string {
	t.Helper()
	var out []string
	for _, e := range r.events(t, github.Ref{}, kind) {
		out = append(out, e.Data)
	}
	return out
}

// wantAndSettle asks for the first turn of each ref and waits for its run.
func (r *rig) wantAndSettle(t *testing.T, refs ...github.Ref) {
	t.Helper()
	for _, ref := range refs {
		r.ready(t, ref, true)
		r.settled()
	}
}

func TestBreakerOpensAndClosesOnATrial(t *testing.T) {
	r := newRig(t, 3)
	r.st.UpsertPR(context.Background(), dinv.PR{Ref: refN(4), State: "OPEN", HeadOid: "h1"})
	for i := 1; i <= 3; i++ {
		r.run.fail[fmt.Sprintf("acme/api#%d", i)] = unavailable
	}
	r.q.Run(context.Background())
	open, since, lastErr, next := r.q.Breaker()
	if !open || !since.Equal(t0) || lastErr != unavailable.Error() || !next.Equal(t0.Add(BreakerCool)) {
		t.Fatalf("breaker %v %v %q %v", open, since, lastErr, next)
	}
	if evs := r.breakerEvents(t, KindBreakerOpened); len(evs) != 1 || evs[0] != unavailable.Error() {
		t.Fatalf("events %v", evs)
	}
	rd := r.ready(t, refN(4), true)
	want := fmt.Sprintf("briefs are unavailable since %s: %s; "+brand.Name+" tries again at %s",
		t0.Local().Format("15:04"), unavailable, t0.Add(BreakerCool).Local().Format("15:04"))
	r.settled()
	if rd.State != Without || rd.Reason != want || r.run.calls != 3 || rd.Cause != intake.CauseBreaker || !rd.Since.Equal(t0) || rd.LastError != unavailable.Error() {
		t.Fatalf("%d calls, %+v", r.run.calls, rd)
	}
	for i := 1; i <= 3; i++ {
		delete(r.run.fail, fmt.Sprintf("acme/api#%d", i))
	}
	r.now = t0.Add(BreakerCool - time.Second)
	r.tick(context.Background())
	if r.run.calls != 3 {
		t.Fatal("a trial before the cool ended")
	}
	r.now = t0.Add(BreakerCool)
	if err := r.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, r.st)
	trial := runs[len(runs)-1]
	if r.run.calls != 4 || trial.Trigger != TriggerTrial || trial.PR != refN(3) || trial.Outcome != OutcomeOK || r.q.isOpen() {
		t.Fatalf("%d calls, trial %+v", r.run.calls, trial)
	}
	if evs := r.breakerEvents(t, KindBreakerClosed); len(evs) != 1 || evs[0] != strconv.FormatInt(trial.ID, 10) {
		t.Fatalf("events %v", evs)
	}
}

func TestNoAgentOpensAtOnce(t *testing.T) {
	r := newRig(t, 2)
	r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
	r.wantAndSettle(t, refN(1))
	if !r.q.isOpen() || len(r.breakerEvents(t, KindBreakerOpened)) != 1 {
		t.Fatal("the missing agent did not open the breaker")
	}
	if rd := r.ready(t, refN(2), true); rd.State != Without || !strings.HasPrefix(rd.Reason, "briefs are unavailable since") {
		t.Fatalf("%+v", rd)
	}
}

func TestAFailedTrialReopens(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
	r.wantAndSettle(t, refN(1))
	r.now = t0.Add(BreakerCool)
	r.tick(context.Background())
	open, since, _, next := r.q.Breaker()
	if r.run.calls != 2 || !open || !since.Equal(t0) || !next.Equal(t0.Add(2*BreakerCool)) {
		t.Fatalf("%d calls, breaker %v %v %v", r.run.calls, open, since, next)
	}
	opened, failed := r.breakerEvents(t, KindBreakerOpened), r.breakerEvents(t, KindBreakerTrialFailed)
	if len(opened) != 1 || len(failed) != 1 || !strings.Contains(failed[0], "not on the PATH") {
		t.Fatalf("opened %v, trials failed %v", opened, failed)
	}
	r.now = t0.Add(BreakerCool + time.Minute)
	r.tick(context.Background())
	if r.run.calls != 2 {
		t.Fatal("tried again before another cool")
	}
}

func TestATrialThatFailsOnItsPullRequestKeepsItOpen(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
	r.wantAndSettle(t, refN(1))
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	r.now = t0.Add(BreakerCool)
	r.tick(context.Background())
	if open, _, _, next := r.q.Breaker(); r.run.calls != 2 || !open || !next.Equal(t0.Add(2*BreakerCool)) {
		t.Fatalf("%d calls, open %v until %v", r.run.calls, open, next)
	}
}

func TestPerPRFailuresNeverOpenIt(t *testing.T) {
	r := newRig(t, 5)
	for i := 1; i <= 5; i++ {
		r.run.fail[fmt.Sprintf("acme/api#%d", i)] = fmt.Errorf("%w: claude: no structured output", turns.ErrUnfit)
	}
	r.q.Run(context.Background())
	if r.run.calls != 5 || r.q.isOpen() || len(r.breakerEvents(t, KindBreakerOpened)) != 0 {
		t.Fatalf("%d calls, open %v", r.run.calls, r.q.isOpen())
	}
}

func TestThreeTimeoutsOnDifferentPRsOpenIt(t *testing.T) {
	r := newRig(t, 3)
	r.q.d.CallTimeout, r.src.block = 20*time.Millisecond, true
	r.q.Run(context.Background())
	if !r.q.isOpen() {
		t.Fatal("three timeouts on three pull requests left it closed")
	}
	failures := 0
	for i := 1; i <= 3; i++ {
		failures += r.record(t, refN(i)).Failures
	}
	if failures != 2 {
		t.Fatalf("%d failures counted toward the pull requests", failures)
	}
}

func TestTimeoutsOfOnePRDoNotOpenIt(t *testing.T) {
	r := newRig(t, 1)
	r.q.d.WantedTimeout, r.src.block = 20*time.Millisecond, true
	r.wantAndSettle(t, refN(1))
	for _, at := range []time.Duration{time.Minute, 5 * time.Minute} {
		r.now = t0.Add(at)
		r.wantAndSettle(t, refN(1))
	}
	if r.q.isOpen() || !strings.HasPrefix(r.ready(t, refN(1), true).Reason, "the brief gave up after 3 attempts") {
		t.Fatal("one slow pull request opened the breaker or did not give up")
	}
}

func TestGlobalFailuresDoNotCountTowardAPR(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = unavailable
	r.wantAndSettle(t, refN(1))
	for _, step := range []struct {
		at, retry time.Duration
		attempt   int
	}{
		{0, time.Minute, 2},
		{time.Minute, 5 * time.Minute, 3},
	} {
		r.now = t0.Add(step.at)
		rec := r.record(t, refN(1))
		if rec.Failures != 0 || !rec.RetryAt.Equal(t0.Add(step.retry)) || !rec.GaveUpAt.IsZero() || r.q.isOpen() {
			t.Fatalf("at %v: %+v", step.at, rec)
		}
		rd := r.ready(t, refN(1), true)
		if rd.State != Wait || rd.Wait != WaitRetry || rd.Attempt != step.attempt || rd.LastError != unavailable.Error() {
			t.Fatalf("at %v: %+v", step.at, rd)
		}
		r.now = t0.Add(step.retry)
		r.wantAndSettle(t, refN(1))
	}
	for range BreakerAfter - 1 {
		if r.q.isOpen() {
			t.Fatal("opened before the wanted schedule and BreakerAfter failures")
		}
		r.wantAndSettle(t, refN(1))
	}
	if rd := r.ready(t, refN(1), true); rd.State != Without || !strings.HasPrefix(rd.Reason, "briefs are unavailable since") {
		t.Fatalf("%+v", rd)
	}
	if rec := r.record(t, refN(1)); rec.Failures != 0 || !rec.GaveUpAt.IsZero() {
		t.Fatalf("%+v", rec)
	}
}

func TestABlipAfterARecoveryRetriesAgain(t *testing.T) {
	r := newRig(t, 2)
	r.run.fail["acme/api#1"] = unavailable
	r.run.fail["acme/api#2"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	r.wantAndSettle(t, refN(1))
	for _, at := range []time.Duration{time.Minute, 5 * time.Minute, 5 * time.Minute} {
		r.now = t0.Add(at)
		r.wantAndSettle(t, refN(1))
	}
	delete(r.run.fail, "acme/api#2")
	p, _, _ := r.st.PR(context.Background(), refN(2))
	r.q.claim(p.Ref)
	if err := r.q.brief(context.Background(), p, TriggerBackground, time.Minute); err != nil {
		t.Fatal(err)
	}
	r.now = t0.Add(6 * time.Minute)
	r.wantAndSettle(t, refN(1))
	rd := r.ready(t, refN(1), true)
	if r.q.isOpen() || rd.State != Wait || rd.Wait != WaitRetry || !rd.Next.Equal(r.now.Add(time.Minute)) {
		t.Fatalf("after the recovery a blip did not retry on the schedule: %+v", rd)
	}
}

func TestTheTrialWaitsForTheWantedSemaphore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, 1)
		r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
		r.q.Run(context.Background())
		delete(r.run.fail, "acme/api#1")
		for range WantedConcurrency {
			r.q.wantedSem <- struct{}{}
		}
		r.now = t0.Add(BreakerCool)
		if _, _, err := r.q.scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		r.run.mu.Lock()
		calls := r.run.calls
		r.run.mu.Unlock()
		if calls != 1 {
			t.Fatalf("the trial ran past a full semaphore: %d calls", calls)
		}
		for range WantedConcurrency {
			<-r.q.wantedSem
		}
		r.settled()
		if r.run.calls != 2 || r.q.isOpen() {
			t.Fatalf("%d calls, open %v", r.run.calls, r.q.isOpen())
		}
	})
}

func TestABlipLeavesTheBreakerClosed(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = unavailable
	r.wantAndSettle(t, refN(1))
	delete(r.run.fail, "acme/api#1")
	r.tick(context.Background())
	r.settled()
	if r.run.calls != 1 {
		t.Fatalf("%d calls before the retry", r.run.calls)
	}
	r.now = t0.Add(time.Minute)
	r.wantAndSettle(t, refN(1))
	if rd := r.ready(t, refN(1), true); rd.State != Ready || r.q.isOpen() || len(r.breakerEvents(t, KindBreakerOpened)) != 0 {
		t.Fatalf("%+v", rd)
	}
}

func TestAPerPRFailureEndsTheRunOfGlobalOnes(t *testing.T) {
	r := newRig(t, 5)
	for i, err := range []error{unavailable, fmt.Errorf("%w: kind %q", ErrInvalid, "epic"), unavailable, fmt.Errorf("%w: kind %q", ErrInvalid, "epic"), unavailable} {
		r.run.fail[fmt.Sprintf("acme/api#%d", i+1)] = err
	}
	r.q.d.Allow = nil
	for i := 1; i <= 5; i++ {
		p, _, _ := r.st.PR(context.Background(), refN(i))
		if !r.q.claim(p.Ref) {
			t.Fatal("claimed")
		}
		r.q.brief(context.Background(), p, TriggerBackground, time.Minute)
	}
	if r.q.isOpen() {
		t.Fatal("three global failures that were not consecutive opened it")
	}
}

func TestTwoTimeoutsOfOnePRDoNotMakeAThirdGlobal(t *testing.T) {
	r := newRig(t, 2)
	r.q.d.CallTimeout, r.src.block = 20*time.Millisecond, true
	for _, n := range []int{1, 1, 2} {
		p, _, _ := r.st.PR(context.Background(), refN(n))
		r.q.claim(p.Ref)
		r.q.brief(context.Background(), p, TriggerBackground, r.q.timeout(false))
	}
	if r.q.isOpen() {
		t.Fatal("timeouts of A, A and B opened it")
	}
}

func TestTheTrialTriesTheNextCandidate(t *testing.T) {
	r := newRig(t, 2)
	r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
	r.q.Run(context.Background())
	delete(r.run.fail, "acme/api#1")
	r.q.claim(refN(2))
	r.now = t0.Add(BreakerCool)
	r.tick(context.Background())
	runs := runsOf(t, r.st)
	if trial := runs[len(runs)-1]; trial.Trigger != TriggerTrial || trial.PR != refN(1) || r.q.isOpen() {
		t.Fatalf("trial %+v", trial)
	}
}

func TestAFirstTurnServesAsTheTrial(t *testing.T) {
	r := newRig(t, 2)
	r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
	r.q.Run(context.Background())
	delete(r.run.fail, "acme/api#1")
	ctx := context.Background()
	p, _, _ := r.st.PR(ctx, refN(2))
	p.Reasons = nil
	r.st.UpsertPR(ctx, p)
	if rd := r.ready(t, refN(2), true); rd.State != Without {
		t.Fatalf("before the cool: %+v", rd)
	}
	r.now = t0.Add(BreakerCool)
	if rd := r.ready(t, refN(2), true); rd.State != Wait || rd.Wait != WaitBriefing {
		t.Fatalf("%+v", rd)
	}
	r.settled()
	runs := runsOf(t, r.st)
	if trial := runs[len(runs)-1]; trial.Trigger != TriggerTrial || trial.PR != refN(2) || r.q.isOpen() {
		t.Fatalf("trial %+v", trial)
	}
	if rd := r.ready(t, refN(2), true); rd.State != Ready {
		t.Fatalf("%+v", rd)
	}
}
func TestBackgroundStopsWhileOpen(t *testing.T) {
	r := newRig(t, 3)
	r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
	r.q.Run(context.Background())
	delete(r.run.fail, "acme/api#1")
	r.now = t0.Add(time.Minute)
	summary, err := r.q.Run(context.Background())
	r.tick(context.Background())
	if err != nil || r.run.calls != 1 || !strings.Contains(summary, "unavailable") {
		t.Fatalf("%d calls, %q %v", r.run.calls, summary, err)
	}
	if rd := r.ready(t, refN(2), false); rd.State != Without || rd.Reason != ReasonNone {
		t.Fatalf("%+v", rd)
	}
	r.settled()
	if r.run.calls != 1 {
		t.Fatalf("a later turn briefed while open: %d calls", r.run.calls)
	}
}

func TestStartEndsWithItsContext(t *testing.T) {
	r := newRig(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.q.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not end")
	}
}

func TestATrialCarriesThePriorityOfItsWaiter(t *testing.T) {
	t.Run("no waiter", func(t *testing.T) {
		r := newRig(t, 2)
		r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
		r.q.Run(context.Background())
		delete(r.run.fail, "acme/api#1")
		r.src.facts = nil
		r.now = t0.Add(BreakerCool)
		r.tick(context.Background())
		r.settled()
		if !slices.Equal(r.src.facts, []dgh.Priority{dgh.PriorityBackground}) {
			t.Fatalf("facts %v", r.src.facts)
		}
	})
	t.Run("a first turn waits", func(t *testing.T) {
		r := newRig(t, 2)
		r.run.fail["acme/api#1"] = fmt.Errorf("claude: %w", turns.ErrNoAgent)
		r.q.Run(context.Background())
		delete(r.run.fail, "acme/api#1")
		r.src.facts = nil
		r.now = t0.Add(BreakerCool)
		if rd := r.ready(t, refN(2), true); rd.State != Wait {
			t.Fatalf("%+v", rd)
		}
		r.settled()
		if !slices.Equal(r.src.facts, []dgh.Priority{dgh.PriorityUser}) {
			t.Fatalf("facts %v", r.src.facts)
		}
	})
}
