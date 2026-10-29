package brief

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/github"
)

func refN(n int) github.Ref { return github.Ref{Owner: "acme", Repo: "api", Number: n} }

func (r *rig) ready(t *testing.T, ref github.Ref, first bool) Readiness {
	t.Helper()
	rd, err := r.q.readyPR(context.Background(), ref, first)
	if err != nil {
		t.Fatal(err)
	}
	return rd
}

// settled waits for the runs Ready started.
func (r *rig) settled() { r.q.wg.Wait() }

// tick runs one of Start's scans and waits for the runs it started.
func (r *rig) tick(ctx context.Context) error {
	_, _, err := r.q.scan(ctx)
	r.settled()
	return err
}

func (r *rig) record(t *testing.T, ref github.Ref) dinv.BriefRecord {
	t.Helper()
	rec, _, err := r.st.BriefRecord(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func (r *rig) events(t *testing.T, ref github.Ref, kind string) []dinv.Event {
	t.Helper()
	evs, err := r.st.Events(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var out []dinv.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func closes(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("Changed did not close")
	}
}

func TestFirstTurnWaitsAndWantsTheBrief(t *testing.T) {
	r := newRig(t, 1)
	changed, release := r.q.Changed(refN(1).Unit())
	defer release()
	if rd := r.ready(t, refN(1), true); rd.State != Wait || rd.Wait != WaitBriefing || rd.Attempt != 1 || rd.LastError != "" {
		t.Fatalf("%+v", rd)
	}
	closes(t, changed)
	r.settled()
	runs := runsOf(t, r.st)
	if len(runs) != 1 || runs[0].Trigger != TriggerWanted || runs[0].Outcome != OutcomeOK {
		t.Fatalf("runs %+v", runs)
	}
	if evs := r.events(t, refN(1), dinv.KindBriefWanted); len(evs) != 1 || !evs[0].At.Equal(t0) {
		t.Fatalf("events %+v", evs)
	}
	rd := r.ready(t, refN(1), true)
	if rd.State != Ready || rd.Brief == nil || rd.Brief.Goal != "g" || rd.Run != runs[0].ID {
		t.Fatalf("%+v", rd)
	}
	if rec := r.record(t, refN(1)); !rec.WantedAt.IsZero() {
		t.Fatalf("the want outlived the start: %+v", rec)
	}
}

func TestReadyJoinsTheBackgroundCall(t *testing.T) {
	r := newRig(t, 1)
	entered, unblock, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r.run.takes = func() {
		close(entered)
		<-unblock
	}
	go func() {
		r.q.Run(context.Background())
		close(done)
	}()
	<-entered
	changed, release := r.q.Changed(refN(1).Unit())
	defer release()
	if rd := r.ready(t, refN(1), true); rd.State != Wait || rd.Wait != WaitBriefing || rd.Attempt != 1 {
		t.Fatalf("%+v", rd)
	}
	close(unblock)
	closes(t, changed)
	<-done
	r.settled()
	if r.run.calls != 1 {
		t.Fatalf("%d calls", r.run.calls)
	}
	if rd := r.ready(t, refN(1), true); rd.State != Ready || rd.Brief == nil {
		t.Fatalf("%+v", rd)
	}
}

func TestLaterTurnTakesAStaleBrief(t *testing.T) {
	r := newRig(t, 2)
	r.src.edits = true
	ctx := context.Background()
	if rd := r.ready(t, refN(2), false); rd.State != Without || rd.Reason != ReasonNone {
		t.Fatalf("no brief: %+v", rd)
	}
	r.q.Run(ctx)
	r.settled()
	calls := r.run.calls
	p, _, _ := inventorytest.Of(r.st).PR(ctx, refN(1))
	p.BodyEditedAt = t0.Add(time.Minute)
	inventorytest.Of(r.st).UpsertPR(ctx, p)
	r.now = t0.Add(time.Minute + BodyQuiet)
	rd := r.ready(t, refN(1), false)
	if rd.State != Ready || rd.Brief == nil || rd.Run == 0 {
		t.Fatalf("%+v", rd)
	}
	r.settled()
	runs := runsOf(t, r.st)
	if r.run.calls != calls+1 || runs[len(runs)-1].Trigger != TriggerRefresh {
		t.Fatalf("no refresh: %d calls, runs %+v", r.run.calls, runs)
	}
	if rec := r.record(t, refN(1)); !rec.WantedAt.IsZero() {
		t.Fatalf("a later turn wanted the brief: %+v", rec)
	}
}

func TestWantedCallTimesOutAt30s(t *testing.T) {
	r := newRig(t, 1)
	if r.q.timeout(true) != 30*time.Second || r.q.timeout(false) != CallTimeout {
		t.Fatalf("timeouts %v %v", r.q.timeout(true), r.q.timeout(false))
	}
	r.q.d.WantedTimeout, r.src.block = 20*time.Millisecond, true
	r.ready(t, refN(1), true)
	r.settled()
	runs := runsOf(t, r.st)
	if len(runs) != 1 || runs[0].Outcome != OutcomeTimeout || runs[0].Trigger != TriggerWanted {
		t.Fatalf("runs %+v", runs)
	}
	if rd := r.ready(t, refN(1), true); rd.State != Wait || rd.Wait != WaitRetry || rd.Attempt != 2 || !rd.Next.Equal(t0.Add(time.Minute)) || rd.LastError == "" {
		t.Fatalf("%+v", rd)
	}
}

func TestATimedOutRunSaysItTimedOut(t *testing.T) {
	r := newRig(t, 1)
	r.q.d.WantedTimeout, r.src.block = 20*time.Millisecond, true
	r.ready(t, refN(1), true)
	r.settled()
	const want = "timed out after 20ms"
	if runs := runsOf(t, r.st); len(runs) != 1 || runs[0].Error != want {
		t.Fatalf("runs %+v", runs)
	}
	if rd := r.ready(t, refN(1), true); rd.LastError != want {
		t.Fatalf("%+v", rd)
	}
}

func TestAWantedRunUnwantedWhileQueuedDoesNotRun(t *testing.T) {
	r := newRig(t, 1)
	for range WantedConcurrency {
		r.q.wantedSem <- struct{}{}
	}
	if rd := r.ready(t, refN(1), true); rd.State != Wait {
		t.Fatalf("%+v", rd)
	}
	if err := r.q.unwantPR(context.Background(), refN(1)); err != nil {
		t.Fatal(err)
	}
	for range WantedConcurrency {
		<-r.q.wantedSem
	}
	r.settled()
	if r.run.calls != 0 || len(runsOf(t, r.st)) != 0 || r.q.inFlight(refN(1)) {
		t.Fatalf("%d calls, runs %+v, in flight %v", r.run.calls, runsOf(t, r.st), r.q.inFlight(refN(1)))
	}
}

func TestAttemptCountsTheGlobalTriesAsReadyDoes(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = unavailable
	r.ready(t, refN(1), true)
	r.settled()
	rd := r.ready(t, refN(1), true)
	n, err := r.q.Attempt(context.Background(), refN(1))
	if err != nil || rd.Attempt != 2 || n != rd.Attempt {
		t.Fatalf("Attempt %d %v, Ready %+v", n, err, rd)
	}
}

func TestRetriesFollowTheWantedSchedule(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	r.ready(t, refN(1), true)
	r.settled()
	for _, step := range []struct {
		at      time.Duration
		wait    string
		attempt int
		next    time.Duration
	}{
		{30 * time.Second, WaitRetry, 2, time.Minute},
		{time.Minute, WaitBriefing, 2, 0},
		{4 * time.Minute, WaitRetry, 3, 5 * time.Minute},
		{5 * time.Minute, WaitBriefing, 3, 0},
	} {
		r.now = t0.Add(step.at)
		rd := r.ready(t, refN(1), true)
		var next time.Time
		if step.next > 0 {
			next = t0.Add(step.next)
		}
		if rd.State != Wait || rd.Wait != step.wait || rd.Attempt != step.attempt || !rd.Next.Equal(next) || !strings.Contains(rd.LastError, "epic") {
			t.Fatalf("at %v: %+v", step.at, rd)
		}
		r.settled()
	}
	want := `the brief gave up after 3 attempts: the brief does not fit the schema: kind "epic"`
	if rd := r.ready(t, refN(1), true); rd.State != Without || rd.Reason != want || rd.Cause != intake.CauseGaveUp || rd.Attempt != 3 || !strings.Contains(rd.LastError, "epic") {
		t.Fatalf("%+v", rd)
	}
	if evs := r.events(t, refN(1), dinv.KindBriefGaveUp); r.run.calls != 3 || len(evs) != 1 || evs[0].Data != want {
		t.Fatalf("%d calls, events %+v", r.run.calls, evs)
	}
	if rec := r.record(t, refN(1)); !rec.WantedAt.IsZero() || !rec.GaveUpAt.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("%+v", rec)
	}
}

func TestTooLongGivesUpAtOnce(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("%w: claude: Prompt is too long", turns.ErrTooLong),
		fmt.Errorf("%w: claude: Exceeded USD budget (0.25)", turns.ErrOverBudget),
	} {
		r := newRig(t, 1)
		r.run.fail["acme/api#1"] = err
		r.ready(t, refN(1), true)
		r.settled()
		if rd := r.ready(t, refN(1), true); rd.State != Without || rd.Reason != "the brief gave up after 1 attempt: "+err.Error() || r.run.calls != 1 {
			t.Fatalf("%v: %d calls, %+v", err, r.run.calls, rd)
		}
	}
}

func TestANewHeadClearsTheGiveUp(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: claude: Prompt is too long", turns.ErrTooLong)
	r.ready(t, refN(1), true)
	r.settled()
	delete(r.run.fail, "acme/api#1")
	p, _, _ := inventorytest.Of(r.st).PR(ctx, refN(1))
	p.HeadOid = "h2"
	inventorytest.Of(r.st).UpsertPR(ctx, p)
	if rd := r.ready(t, refN(1), true); rd.State != Wait || rd.Attempt != 1 || rd.LastError != "" {
		t.Fatalf("%+v", rd)
	}
	r.settled()
	if rd := r.ready(t, refN(1), true); rd.State != Ready {
		t.Fatalf("%+v", rd)
	}
	if rec := r.record(t, refN(1)); !rec.GaveUpAt.IsZero() || rec.GaveUpReason != "" || rec.Failures != 0 {
		t.Fatalf("%+v", rec)
	}
}

func TestOffGoesWithout(t *testing.T) {
	r := newRig(t, 1)
	r.q.d.Enabled = false
	for _, first := range []bool{true, false} {
		if rd := r.ready(t, refN(1), first); rd.State != Without || rd.Cause != intake.CauseOff || rd.Reason != ReasonOff {
			t.Fatalf("%+v", rd)
		}
	}
	r.settled()
	if r.run.calls != 0 {
		t.Fatalf("%d calls", r.run.calls)
	}
}

func TestIntakesAnswerTheIntakesPort(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	a := Intakes{Q: r.q}
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 1}
	changed, release := a.Changed(context.Background(), ref.Unit())
	defer release()
	if rd, err := a.Ready(context.Background(), ref.Unit(), true, nil); err != nil || rd != (intake.Readiness{Wait: WaitBriefing, Attempt: 1}) {
		t.Fatalf("%+v %v", rd, err)
	}
	closes(t, changed)
	r.settled()
	rd, err := a.Ready(context.Background(), ref.Unit(), true, nil)
	if want := (intake.Readiness{Wait: WaitRetry, Attempt: 2, LastError: `the brief does not fit the schema: kind "epic"`, Next: t0.Add(time.Minute)}); err != nil || rd != want {
		t.Fatalf("%+v %v", rd, err)
	}
	r.q.d.Enabled = false
	if rd, _ := a.Ready(context.Background(), ref.Unit(), true, nil); rd != (intake.Readiness{Go: true, Without: true, Cause: intake.CauseOff, Note: ReasonOff}) {
		t.Fatalf("%+v", rd)
	}
}

func TestABackgroundBatchSkipsAPullRequestAWantedRunBriefed(t *testing.T) {
	r := newRig(t, 2)
	hold := make(chan struct{})
	r.src.hold = map[string]chan struct{}{"acme/api#1": hold}
	done := make(chan struct{})
	go func() {
		r.q.Run(context.Background())
		close(done)
	}()
	<-hold
	r.ready(t, refN(2), true)
	r.settled()
	r.ready(t, refN(2), true)
	hold <- struct{}{}
	<-done
	if r.run.calls != 2 {
		t.Fatalf("%d calls: the batch briefed again what the wanted run briefed", r.run.calls)
	}
}

func TestAFirstTurnOfAnUncollectedPRWaitsForItsBrief(t *testing.T) {
	r := newRig(t, 0)
	ref := github.Ref{Owner: "acme", Repo: "web", Number: 9}
	if rd := r.ready(t, ref, true); rd.State != Wait || rd.Wait != WaitBriefing {
		t.Fatalf("%+v", rd)
	}
	r.settled()
	if rd := r.ready(t, ref, true); rd.State != Ready || rd.Brief == nil || r.run.calls != 1 {
		t.Fatalf("%d calls, %+v", r.run.calls, rd)
	}
}

func TestUnwantDropsTheWant(t *testing.T) {
	r := newRig(t, 1)
	r.run.fail["acme/api#1"] = fmt.Errorf("%w: kind %q", ErrInvalid, "epic")
	r.ready(t, refN(1), true)
	r.settled()
	if err := (Intakes{Q: r.q}).Unwant(context.Background(), pr.Ref{Owner: "acme", Repo: "api", Number: 1}.Unit()); err != nil {
		t.Fatal(err)
	}
	r.now = t0.Add(time.Hour)
	r.tick(context.Background())
	r.settled()
	if rec := r.record(t, refN(1)); !rec.WantedAt.IsZero() || r.run.calls != 1 {
		t.Fatalf("%d calls, %+v", r.run.calls, rec)
	}
}

func TestForgetWaitsForARunToSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, 1)
		r.q.state.Lock()
		unlock := sync.OnceFunc(r.q.state.Unlock)
		done := make(chan struct{})
		t.Cleanup(func() {
			unlock()
			<-done
		})
		go func() {
			r.q.Forget(context.Background(), refN(1))
			close(done)
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("Forget ran while a run was settling")
		default:
		}
		unlock()
		<-done
	})
}

func TestWantedRunsAreBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, 3)
		holds := map[string]chan struct{}{}
		for i := 1; i <= 3; i++ {
			holds[fmt.Sprintf("acme/api#%d", i)] = make(chan struct{})
		}
		r.src.hold = holds
		for i := 1; i <= 3; i++ {
			r.ready(t, refN(i), true)
		}
		synctest.Wait()
		var started []string
		for ref, h := range holds {
			select {
			case <-h:
				started = append(started, ref)
			default:
			}
		}
		if len(started) != WantedConcurrency {
			t.Fatalf("runs started: %v", started)
		}
		for _, ref := range started {
			holds[ref] <- struct{}{}
		}
		for ref, h := range holds {
			if !slices.Contains(started, ref) {
				<-h
				h <- struct{}{}
			}
		}
		r.settled()
		if r.run.calls != 3 {
			t.Fatalf("%d calls", r.run.calls)
		}
	})
}

func TestRefreshesDoNotHoldBackAWantedBrief(t *testing.T) {
	r := newRig(t, 1)
	for range WantedConcurrency {
		r.q.refreshSem <- struct{}{}
	}
	r.ready(t, refN(1), true)
	r.settled()
	if r.run.calls != 1 {
		t.Fatalf("%d calls while the refreshes held their slots", r.run.calls)
	}
	for range WantedConcurrency {
		<-r.q.refreshSem
	}
	if got := r.q.timeout(true); got != WantedTimeout {
		t.Fatalf("refresh timeout %v", got)
	}
}

func TestPeekAnswersLikeReadyWithoutStartingOrWriting(t *testing.T) {
	r := newRig(t, 1)
	rd, err := r.q.peekPR(context.Background(), refN(1), true)
	if err != nil {
		t.Fatal(err)
	}
	if rd.State != Wait || rd.Wait != WaitBriefing || rd.Attempt != 1 {
		t.Fatalf("%+v", rd)
	}
	r.settled()
	if runs := runsOf(t, r.st); len(runs) != 0 {
		t.Fatalf("Peek ran a brief: %+v", runs)
	}
	if evs := r.events(t, refN(1), dinv.KindBriefWanted); len(evs) != 0 {
		t.Fatalf("Peek wanted the brief: %+v", evs)
	}
	if rec := r.record(t, refN(1)); !rec.WantedAt.IsZero() {
		t.Fatalf("Peek stored a want: %+v", rec)
	}

	r.ready(t, refN(1), true)
	r.settled()
	if rd, err := r.q.peekPR(context.Background(), refN(1), true); err != nil || rd.State != Ready {
		t.Fatalf("after the brief: %+v %v", rd, err)
	}
	if rd, err := r.q.peekPR(context.Background(), refN(2), false); err != nil || rd.State != Without {
		t.Fatalf("a later turn without a brief: %+v %v", rd, err)
	}
}
