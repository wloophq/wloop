package intaketest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// MakeStore builds an empty store for t.
type MakeStore func(t *testing.T) intake.Store

// Backend is what the scenarios need of a store beyond intake.Store: the
// events its writes stored, in order, and a way to leave an intake as it is.
// A store that can also inject faults has FailWith, and the scenarios that
// need it skip without.
type Backend interface {
	intake.Store
	Events() []port.Event
	Put(i intake.Intake) intake.Intake
}

func backend(t *testing.T, mk MakeStore) Backend {
	t.Helper()
	b, ok := mk(t).(Backend)
	if !ok {
		t.Fatal("the store is not an intaketest.Backend")
	}
	return b
}

// Service runs the intake service's scenarios on stores mk builds.
func Service(t *testing.T, mk MakeStore) {
	for name, run := range map[string]func(*testing.T, MakeStore){
		"ReadyStartsAtOnce":                            testReadyStartsAtOnce,
		"FirstTurnWaitsThenStarts":                     testFirstTurnWaitsThenStarts,
		"RetryWakesOnTheNextTime":                      testRetryWakesOnTheNextTime,
		"GaveUpStartsWithout":                          testGaveUpStartsWithout,
		"BreakerOpenStartsWithout":                     testBreakerOpenStartsWithout,
		"JoinReplacesTheGuidance":                      testJoinReplacesTheGuidance,
		"JoinWithoutGuidanceKeepsIt":                   testJoinWithoutGuidanceKeepsIt,
		"WithoutWhileBriefing":                         testWithoutWhileBriefing,
		"NoBriefStartsWithoutAsking":                   testNoBriefStartsWithoutAsking,
		"CancelWhileBriefing":                          testCancelWhileBriefing,
		"CancelDropsTheWantOnceNoIntakeWaits":          testCancelDropsTheWantOnceNoIntakeWaits,
		"WithoutDropsTheWant":                          testWithoutDropsTheWant,
		"CancelLogsWhatFailsAfterItIsSaved":            testCancelLogsWhatFailsAfterItIsSaved,
		"RecoverResumesBriefingAndFailsStarting":       testRecoverResumesBriefingAndFailsStarting,
		"StarterErrorFails":                            testStarterErrorFails,
		"LogLinesArePublished":                         testLogLinesArePublished,
		"ASkippedLogSaveTrailsWithinASecond":           testASkippedLogSaveTrailsWithinASecond,
		"JoinTwiceKeepsTheLastGuidance":                testJoinTwiceKeepsTheLastGuidance,
		"ARunEndingDuringReadyWakesTheIntake":          testARunEndingDuringReadyWakesTheIntake,
		"ShutdownWhileAskingLeavesTheIntakeWaiting":    testShutdownWhileAskingLeavesTheIntakeWaiting,
		"ShutdownBeforeStartingSendsNoTurn":            testShutdownBeforeStartingSendsNoTurn,
		"ShutdownWhileWaitingKeepsBriefing":            testShutdownWhileWaitingKeepsBriefing,
		"ShutdownAfterTheTurnWasSentStillSavesStarted": testShutdownAfterTheTurnWasSentStillSavesStarted,
		"ReadyErrorFails":                              testReadyErrorFails,
		"FirstErrorFails":                              testFirstErrorFails,
		"StoreErrorsAreReturned":                       testStoreErrorsAreReturned,
		"JoinOnAStartingIntakeChangesNothing":          testJoinOnAStartingIntakeChangesNothing,
		"JoinWithNoBriefStartsWithout":                 testJoinWithNoBriefStartsWithout,
		"CancelOfAStartedIntakeIsRefused":              testCancelOfAStartedIntakeIsRefused,
		"ASecondDriveIsRefused":                        testASecondDriveIsRefused,
		"PanickingStarterReleasesTheLoopMutex":         testPanickingStarterReleasesTheLoopMutex,
		"WaitLogsTheLastFailure":                       testWaitLogsTheLastFailure,
		"LogLinesAreStoredWhileStarting":               testLogLinesAreStoredWhileStarting,
		"AWaitThatDidNotChangeIsNotLoggedAgain":        testAWaitThatDidNotChangeIsNotLoggedAgain,
		"AResumedWaitIsNotLoggedAgain":                 testAResumedWaitIsNotLoggedAgain,
		"BriefsOffStartsWithOff":                       testBriefsOffStartsWithOff,
		"TimesAreInTheClocksZone":                      testTimesAreInTheClocksZone,
		"AFailedBriefingWaitNamesTheFailure":           testAFailedBriefingWaitNamesTheFailure,
		"CanJoinRefusesAJoin":                          testCanJoinRefusesAJoin,
		"CancelForKeepsItsNote":                        testCancelForKeepsItsNote,
		"AStopWhileWaitingForTheLoopLeavesItStarting":  testAStopWhileWaitingForTheLoopLeavesItStarting,
		"AStarterReadsAheadBeforeTheLoopMutex":         testAStarterReadsAheadBeforeTheLoopMutex,
	} {
		t.Run(name, func(t *testing.T) { run(t, mk) })
	}
}

var (
	r7 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	t0 = time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC)

	ready   = intake.Readiness{Go: true}
	briefed = intake.Readiness{Wait: "briefing", Attempt: 1}
)

type env struct {
	s       *intake.Service
	store   Backend
	briefs  *Briefs
	starter *Starter
	pub     *Publisher
	timers  *Timers
	clock   *Clock
	lock    *sync.Mutex
}

func newEnv(t *testing.T, mk MakeStore, first bool, answers ...intake.Readiness) *env {
	e := &env{
		store:   backend(t, mk),
		briefs:  NewBriefs(answers...),
		starter: &Starter{Next: intake.StartedTurn{TurnSeq: 11, TurnID: "r-11"}},
		pub:     &Publisher{},
		timers:  &Timers{},
		clock:   NewClock(t0),
		lock:    &sync.Mutex{},
	}
	e.s = &intake.Service{Store: e.store, Briefs: e.briefs, Firsts: Firsts(first), Starter: e.starter,
		Payloads: Payloads{}, Clock: e.clock, Pub: e.pub, Lock: e.lock, After: e.timers.After}
	return e
}

func (e *env) accept(t *testing.T, r intake.Request) intake.Intake {
	t.Helper()
	if r.Unit.IsZero() {
		r.Unit = r7
	}
	if r.Intent == "" {
		r.Intent = turn.Intent(dintent.Review)
	}
	if r.Source == "" {
		r.Source = "cli"
	}
	i, joined, err := e.s.Accept(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if joined {
		t.Fatalf("intake %d joined, want a new one", i.Seq)
	}
	return i
}

type driven struct {
	i   intake.Intake
	err error
}

func (e *env) drive(seq int64) <-chan driven {
	ch := make(chan driven, 1)
	go func() {
		i, err := e.s.Drive(context.Background(), seq)
		ch <- driven{i, err}
	}()
	return ch
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

func (e *env) waiting(t *testing.T, seq int64) {
	t.Helper()
	eventually(t, "the intake never waited on the brief", func() bool {
		i, _, _ := e.store.Intake(context.Background(), seq)
		return i.State == intake.Briefing
	})
}

func done(t *testing.T, ch <-chan driven) intake.Intake {
	t.Helper()
	select {
	case d := <-ch:
		if d.err != nil {
			t.Fatal(d.err)
		}
		return d.i
	case <-time.After(5 * time.Second):
		t.Fatal("Drive never ended")
		return intake.Intake{}
	}
}

func (e *env) stored(t *testing.T, seq int64) intake.Intake {
	t.Helper()
	i, ok, err := e.store.Intake(context.Background(), seq)
	if err != nil || !ok {
		t.Fatalf("intake %d: %v, %v", seq, ok, err)
	}
	return i
}

func kinds(evs []port.Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Kind != intake.KindLog {
			out = append(out, ev.Kind+":"+ev.Key)
		}
	}
	return out
}

func logs(evs []port.Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Kind == intake.KindLog {
			out = append(out, ev.Key+":"+ev.Data)
		}
	}
	return out
}

func testReadyStartsAtOnce(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	e.starter.Fn = func(_ context.Context, i intake.Intake, _ io.Writer) (intake.StartedTurn, error) {
		if e.lock.TryLock() {
			e.lock.Unlock()
			t.Error("the turn started without the loop mutex")
		}
		if i.State != intake.Starting || i.Brief != intake.BriefReady {
			t.Errorf("started intake %s with brief %q, want starting with ready", i.State, i.Brief)
		}
		return intake.StartedTurn{TurnSeq: 11, TurnID: "r-11"}, nil
	}
	i := e.accept(t, intake.Request{Payload: []byte(`{"pr":7}`)})
	if i.Seq != 1 || i.State != intake.Accepted || !i.AcceptedAt.Equal(t0) {
		t.Fatalf("accepted %+v", i)
	}
	got, err := e.s.Drive(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Started || got.TurnSeq != 11 || got.TurnID != "r-11" || got.EndedAt.IsZero() || got.StartingAt.IsZero() {
		t.Fatalf("drove to %+v", got)
	}
	if s := e.stored(t, i.Seq); s.State != intake.Started || s.TurnSeq != 11 || s.TurnID != "r-11" {
		t.Fatalf("stored %+v", s)
	}
	if want := []string{"intake_accepted:1", "intake_starting:1", "intake_started:1"}; !slices.Equal(kinds(e.store.Events()), want) {
		t.Fatalf("events %v, want %v", kinds(e.store.Events()), want)
	}
	if got := e.briefs.Asked(); !slices.Equal(got, []bool{false}) {
		t.Fatalf("Ready asked with first %v, want [false]", got)
	}
	if len(e.starter.Calls()) != 1 {
		t.Fatalf("started %d turns, want 1", len(e.starter.Calls()))
	}
}

func testFirstTurnWaitsThenStarts(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed, ready)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	s := e.stored(t, i.Seq)
	if s.State != intake.Briefing || s.Note != "briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)" || s.BriefingAt.IsZero() {
		t.Fatalf("while waiting, stored %+v", s)
	}
	if len(e.starter.Calls()) != 0 {
		t.Fatal("the turn started before the brief")
	}
	e.briefs.End(r7)
	got := done(t, ch)
	if got.State != intake.Started || got.Brief != intake.BriefReady {
		t.Fatalf("drove to %+v", got)
	}
	if want := []string{"intake_accepted:1", "intake_briefing:1", "intake_starting:1", "intake_started:1"}; !slices.Equal(kinds(e.store.Events()), want) {
		t.Fatalf("events %v, want %v", kinds(e.store.Events()), want)
	}
	if got := e.briefs.Asked(); !slices.Equal(got, []bool{true, true}) {
		t.Fatalf("Ready asked with first %v", got)
	}
}

func testRetryWakesOnTheNextTime(t *testing.T, mk MakeStore) {
	retry := intake.Readiness{Wait: "retry", Attempt: 2, LastError: "timeout", Next: t0.Add(time.Minute)}
	e := newEnv(t, mk, true, retry, ready)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	eventually(t, "no timer was asked", func() bool { return len(e.timers.Asked()) > 0 })
	if got := e.timers.Asked(); !slices.Equal(got, []time.Duration{time.Minute}) {
		t.Fatalf("timers %v, want one of a minute", got)
	}
	if s := e.stored(t, i.Seq); s.Note != "brief attempt 1 failed: timeout; next attempt at 13:01" {
		t.Fatalf("note %q", s.Note)
	}
	e.timers.Fire()
	if got := done(t, ch); got.State != intake.Started {
		t.Fatalf("drove to %+v", got)
	}
}

func testGaveUpStartsWithout(t *testing.T, mk MakeStore) {
	gaveUp := intake.Readiness{Go: true, Without: true, Cause: intake.CauseGaveUp, Attempt: 3, LastError: "timeout", Note: "the brief gave up after 3 attempts: timeout"}
	e := newEnv(t, mk, true, briefed, gaveUp)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	e.briefs.End(r7)
	got := done(t, ch)
	if got.State != intake.Started || got.Brief != intake.BriefWithout || got.Note != gaveUp.Note {
		t.Fatalf("drove to %+v", got)
	}
	if c := e.starter.Calls(); len(c) != 1 || c[0].Brief != intake.BriefWithout {
		t.Fatalf("started %+v", c)
	}
	if !slices.Contains(got.Log, "brief gave up after 3 attempts: timeout; starting without it") {
		t.Fatalf("log %q", got.Log)
	}
	if evs := e.store.Events(); evs[len(evs)-2].Kind != intake.KindStarting || evs[len(evs)-2].Data != gaveUp.Note {
		t.Fatalf("starting event %+v", evs[len(evs)-2])
	}
}

func testBreakerOpenStartsWithout(t *testing.T, mk MakeStore) {
	open := intake.Readiness{Go: true, Without: true, Cause: intake.CauseBreaker, Since: t0.Add(-20 * time.Minute), LastError: "rate limited",
		Note: "briefs are unavailable since 12:40: rate limited; " + brand.Name + " tries again at 12:55"}
	e := newEnv(t, mk, true, open)
	i := e.accept(t, intake.Request{})
	got, err := e.s.Drive(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Started || got.Brief != intake.BriefWithout || got.Note != open.Note || !got.BriefingAt.IsZero() {
		t.Fatalf("drove to %+v", got)
	}
	if !slices.Contains(got.Log, "briefs are unavailable since 12:40: rate limited; starting without the brief") {
		t.Fatalf("log %v lacks the breaker's line", got.Log)
	}
}

func testJoinReplacesTheGuidance(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{Payload: []byte("guidance:first")})
	got, joined, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "session", Guidance: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if !joined || got.Seq != i.Seq {
		t.Fatalf("joined %v intake %d, want intake %d joined", joined, got.Seq, i.Seq)
	}
	s := e.stored(t, i.Seq)
	if string(s.Request) != "guidance:second" || s.Note != intake.NoteGuidance || s.Source != "cli" {
		t.Fatalf("stored %+v", s)
	}
	evs := e.store.Events()
	if last := evs[len(evs)-1]; last.Kind != intake.KindGuidance || last.Key != "1:1" || last.Data != "guidance replaced by a later request" {
		t.Fatalf("last event %+v", last)
	}
	other, joined, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Address), Source: "cli"})
	if err != nil || joined || other.Seq == i.Seq {
		t.Fatalf("an address request joined the review intake: %v %v", joined, err)
	}
}

func testJoinWithoutGuidanceKeepsIt(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{Payload: []byte("guidance:first")})
	before := len(e.store.Events())
	got, joined, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cockpit"})
	if err != nil || !joined || got.Seq != i.Seq {
		t.Fatalf("join: %v %v %d", joined, err, got.Seq)
	}
	if s := e.stored(t, i.Seq); string(s.Request) != "guidance:first" || s.Note != "" {
		t.Fatalf("stored %+v", s)
	}
	if len(e.store.Events()) != before {
		t.Fatalf("a join without guidance wrote %v", kinds(e.store.Events()[before:]))
	}
}

func testWithoutWhileBriefing(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	if err := e.s.Without(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
	got := done(t, ch)
	if got.State != intake.Started || got.Brief != intake.BriefWithout || got.Note != "you started it without the brief" {
		t.Fatalf("drove to %+v", got)
	}
	if n := len(e.briefs.Asked()); n != 1 {
		t.Fatalf("Ready asked %d times, want 1", n)
	}
	if err := e.s.Without(context.Background(), i.Seq); !errors.Is(err, intake.ErrNotWaiting) {
		t.Fatalf("Without of a started intake: %v", err)
	}
}

func testNoBriefStartsWithoutAsking(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{NoBrief: true})
	got, err := e.s.Drive(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Started || got.Brief != intake.BriefWithout || got.Note != "asked to start without the brief" {
		t.Fatalf("drove to %+v", got)
	}
	if n := len(e.briefs.Asked()); n != 0 {
		t.Fatalf("Ready asked %d times, want none", n)
	}
}

func testCancelWhileBriefing(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	if err := e.s.Cancel(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
	got := done(t, ch)
	if got.State != intake.Canceled || got.EndedAt.IsZero() || got.Note != "canceled by the user" {
		t.Fatalf("drove to %+v", got)
	}
	if evs := e.store.Events(); evs[len(evs)-1].Data != "canceled by the user" {
		t.Fatalf("the cancel event carries %q", evs[len(evs)-1].Data)
	}
	if len(e.starter.Calls()) != 0 {
		t.Fatal("a canceled intake started its turn")
	}
	if want := []string{"intake_accepted:1", "intake_briefing:1", "intake_canceled:1"}; !slices.Equal(kinds(e.store.Events()), want) {
		t.Fatalf("events %v, want %v", kinds(e.store.Events()), want)
	}
	again := e.accept(t, intake.Request{})
	if again.Seq == i.Seq {
		t.Fatal("a request after the cancel joined the canceled intake")
	}
	if err := e.s.Cancel(context.Background(), 99); !errors.Is(err, intake.ErrUnknown) {
		t.Fatalf("Cancel of an unknown intake: %v", err)
	}
}

func testCancelDropsTheWantOnceNoIntakeWaits(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	review := e.accept(t, intake.Request{})
	address := e.accept(t, intake.Request{Intent: turn.Intent(dintent.Address)})
	if err := e.s.Cancel(context.Background(), review.Seq); err != nil {
		t.Fatal(err)
	}
	if got := e.briefs.Unwanted(); len(got) != 0 {
		t.Fatalf("dropped the want while address still waits: %v", got)
	}
	if err := e.s.Cancel(context.Background(), address.Seq); err != nil {
		t.Fatal(err)
	}
	if got := e.briefs.Unwanted(); !slices.Equal(got, []unit.Unit{r7}) {
		t.Fatalf("unwanted %v", got)
	}
}

func testWithoutDropsTheWant(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{})
	if err := e.s.Without(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
	if got := e.briefs.Unwanted(); !slices.Equal(got, []unit.Unit{r7}) {
		t.Fatalf("unwanted %v", got)
	}
}

func testCancelLogsWhatFailsAfterItIsSaved(t *testing.T, mk MakeStore) {
	boom := errors.New("disk gone")
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, e *env)
	}{
		{"open", func(t *testing.T, e *env) {
			faulty, ok := e.store.(interface {
				FailWith(func(op string, i intake.Intake) error)
			})
			if !ok {
				t.Skip("the store cannot inject faults")
			}
			faulty.FailWith(func(op string, _ intake.Intake) error {
				if op == "open" {
					return boom
				}
				return nil
			})
		}},
		{"unwant", func(_ *testing.T, e *env) { e.briefs.UnwantErr = boom }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, mk, true, briefed)
			var mu sync.Mutex
			var lines []string
			e.s.Log = func(f string, a ...any) {
				mu.Lock()
				defer mu.Unlock()
				lines = append(lines, fmt.Sprintf(f, a...))
			}
			i := e.accept(t, intake.Request{})
			c.setup(t, e)
			if err := e.s.Cancel(context.Background(), i.Seq); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if got := e.stored(t, i.Seq); got.State != intake.Canceled || len(lines) != 1 || !strings.Contains(lines[0], "disk gone") {
				t.Fatalf("%+v, logs %v", got, lines)
			}
		})
	}
}

func testRecoverResumesBriefingAndFailsStarting(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, ready)
	briefing := e.store.Put(intake.Intake{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", State: intake.Briefing, AcceptedAt: t0, BriefingAt: t0})
	starting := e.store.Put(intake.Intake{Unit: r7, Intent: turn.Intent(dintent.Address), Source: "cli", State: intake.Starting, AcceptedAt: t0, StartingAt: t0})
	e.store.Put(intake.Intake{Unit: r7, Intent: turn.Intent(dintent.Nudge), Source: "cli", State: intake.Started, AcceptedAt: t0})

	again, err := e.s.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(again, []int64{briefing.Seq}) {
		t.Fatalf("Recover answered %v, want [%d]", again, briefing.Seq)
	}
	s := e.stored(t, starting.Seq)
	if s.State != intake.Failed || s.Error != "the daemon stopped while starting the turn; ask again" || s.EndedAt.IsZero() {
		t.Fatalf("the starting intake is %+v", s)
	}
	if got, err := e.s.Drive(context.Background(), starting.Seq); err != nil || got.State != intake.Failed {
		t.Fatalf("driving the failed intake: %+v, %v", got, err)
	}
	got, err := e.s.Drive(context.Background(), briefing.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Started {
		t.Fatalf("the briefing intake drove to %+v", got)
	}
	calls := e.starter.Calls()
	if len(calls) != 1 || calls[0].Seq != briefing.Seq {
		t.Fatalf("started %+v, want only intake %d", calls, briefing.Seq)
	}
	want := []string{fmt.Sprintf("intake_failed:%d", starting.Seq), fmt.Sprintf("intake_starting:%d", briefing.Seq), fmt.Sprintf("intake_started:%d", briefing.Seq)}
	if !slices.Equal(kinds(e.store.Events()), want) {
		t.Fatalf("events %v, want %v", kinds(e.store.Events()), want)
	}
}

func testStarterErrorFails(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	e.starter.Fn = func(_ context.Context, _ intake.Intake, out io.Writer) (intake.StartedTurn, error) {
		fmt.Fprintln(out, "sending the review")
		return intake.StartedTurn{}, errors.New("the workspace is gone")
	}
	i := e.accept(t, intake.Request{})
	got, err := e.s.Drive(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Failed || got.Error != "the workspace is gone" || got.TurnSeq != 0 {
		t.Fatalf("drove to %+v", got)
	}
	evs := e.store.Events()
	if last := evs[len(evs)-1]; last.Kind != intake.KindFailed || last.Data != "the workspace is gone" {
		t.Fatalf("last event %+v", last)
	}
	if s := e.stored(t, i.Seq); !slices.Equal(s.Log, []string{"sending the review"}) {
		t.Fatalf("log %q", s.Log)
	}
	if !e.lock.TryLock() {
		t.Fatal("the loop mutex is still held")
	}
}

func testASkippedLogSaveTrailsWithinASecond(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, ready)
	release := make(chan struct{})
	e.starter.Fn = func(_ context.Context, _ intake.Intake, out io.Writer) (intake.StartedTurn, error) {
		fmt.Fprintln(out, "first")
		fmt.Fprintln(out, "second")
		<-release
		return intake.StartedTurn{TurnSeq: 3, TurnID: "r-3"}, nil
	}
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	eventually(t, "no trailing save was armed", func() bool { return len(e.timers.Asked()) > 0 })
	if got := e.timers.Asked(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Fatalf("timers %v, want one of a second", got)
	}
	if s := e.stored(t, i.Seq); !slices.Equal(s.Log, []string{"first"}) {
		t.Fatalf("before the timer, stored log %q", s.Log)
	}
	e.timers.Fire()
	eventually(t, "the second line was never stored", func() bool {
		return slices.Equal(e.stored(t, i.Seq).Log, []string{"first", "second"})
	})
	close(release)
	done(t, ch)
}

func testLogLinesArePublished(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed, ready)
	var seen []string
	e.starter.Fn = func(_ context.Context, _ intake.Intake, out io.Writer) (intake.StartedTurn, error) {
		fmt.Fprint(out, "starting the review\nposting ")
		seen = logs(e.pub.Events())
		fmt.Fprint(out, "the round")
		return intake.StartedTurn{TurnSeq: 3, TurnID: "r-3"}, nil
	}
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	e.briefs.End(r7)
	got := done(t, ch)

	if !slices.Contains(seen, "1:starting the review") {
		t.Fatalf("while the action ran, published %v", seen)
	}
	want := []string{"1:briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)", "1:starting the review", "1:posting the round"}
	if g := logs(e.pub.Events()); !slices.Equal(g, want) {
		t.Fatalf("published %v, want %v", g, want)
	}
	if !slices.Equal(got.Log, []string{"briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)", "starting the review", "posting the round"}) {
		t.Fatalf("log %q", got.Log)
	}
	if s := e.stored(t, i.Seq); !slices.Equal(s.Log, got.Log) {
		t.Fatalf("stored log %q", s.Log)
	}
	for _, ev := range e.store.Events() {
		if ev.Kind == intake.KindLog {
			t.Fatalf("a log line was stored: %+v", ev)
		}
	}
	if pubs := kinds(e.pub.Events()); !slices.Equal(pubs, kinds(e.store.Events())) {
		t.Fatalf("published %v, stored %v", pubs, kinds(e.store.Events()))
	}
}

func testJoinTwiceKeepsTheLastGuidance(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{Payload: []byte("guidance:first")})
	for _, g := range []string{"second", "third"} {
		if _, joined, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", Guidance: g}); err != nil || !joined {
			t.Fatalf("join %s: %v %v", g, joined, err)
		}
	}
	if s := e.stored(t, i.Seq); string(s.Request) != "guidance:third" {
		t.Fatalf("stored request %q, want the last guidance", s.Request)
	}
	if want := []string{"intake_accepted:1", "intake_guidance:1:1", "intake_guidance:1:2"}; !slices.Equal(kinds(e.store.Events()), want) {
		t.Fatalf("events %v, want %v", kinds(e.store.Events()), want)
	}
}

func testARunEndingDuringReadyWakesTheIntake(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed, ready)
	var calls int
	e.briefs.OnReady = func(context.Context) {
		if calls++; calls == 1 {
			e.briefs.End(r7)
		}
	}
	i := e.accept(t, intake.Request{})
	if got := done(t, e.drive(i.Seq)); got.State != intake.Started || got.Brief != intake.BriefReady {
		t.Fatalf("drove to %+v", got)
	}
}

func testShutdownWhileAskingLeavesTheIntakeWaiting(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, ready)
	ctx, cancel := context.WithCancel(context.Background())
	e.s.Firsts = FirstsFunc(func(ctx context.Context, _ unit.Unit) (bool, error) {
		cancel()
		return false, ctx.Err()
	})
	i := e.accept(t, intake.Request{})
	if _, err := e.s.Drive(ctx, i.Seq); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drive: %v, want canceled", err)
	}
	if s := e.stored(t, i.Seq); s.State != intake.Accepted || s.Error != "" {
		t.Fatalf("stored %+v, want it still accepted", s)
	}
}

func testShutdownBeforeStartingSendsNoTurn(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, ready)
	ctx, cancel := context.WithCancel(context.Background())
	e.s.Firsts = FirstsFunc(func(context.Context, unit.Unit) (bool, error) {
		cancel()
		return true, nil
	})
	i := e.accept(t, intake.Request{})
	if _, err := e.s.Drive(ctx, i.Seq); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drive: %v, want canceled", err)
	}
	if s := e.stored(t, i.Seq); s.State != intake.Accepted {
		t.Fatalf("stored %+v, want it still accepted", s)
	}
	if len(e.starter.Calls()) != 0 {
		t.Fatal("a turn started after the shutdown")
	}
	if want := []string{"intake_accepted:1"}; !slices.Equal(kinds(e.store.Events()), want) {
		t.Fatalf("events %v, want %v", kinds(e.store.Events()), want)
	}
}

func testShutdownWhileWaitingKeepsBriefing(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	ctx, cancel := context.WithCancel(context.Background())
	i := e.accept(t, intake.Request{})
	ch := make(chan error, 1)
	go func() {
		_, err := e.s.Drive(ctx, i.Seq)
		ch <- err
	}()
	e.waiting(t, i.Seq)
	cancel()
	if err := <-ch; !errors.Is(err, context.Canceled) {
		t.Fatalf("Drive: %v, want canceled", err)
	}
	if s := e.stored(t, i.Seq); s.State != intake.Briefing {
		t.Fatalf("stored %+v, want it still briefing", s)
	}
	again, err := e.s.Recover(context.Background())
	if err != nil || !slices.Equal(again, []int64{i.Seq}) {
		t.Fatalf("Recover: %v %v", again, err)
	}
}

func testShutdownAfterTheTurnWasSentStillSavesStarted(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	ctx, cancel := context.WithCancel(context.Background())
	e.starter.Fn = func(_ context.Context, _ intake.Intake, out io.Writer) (intake.StartedTurn, error) {
		cancel()
		fmt.Fprintln(out, "sent")
		return intake.StartedTurn{TurnSeq: 5, TurnID: "r-5"}, nil
	}
	i := e.accept(t, intake.Request{})
	if _, err := e.s.Drive(ctx, i.Seq); err != nil {
		t.Fatal(err)
	}
	if s := e.stored(t, i.Seq); s.State != intake.Started || s.TurnSeq != 5 || !slices.Equal(s.Log, []string{"sent"}) {
		t.Fatalf("stored %+v", s)
	}
}

func testReadyErrorFails(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true)
	i := e.accept(t, intake.Request{})
	got, err := e.s.Drive(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Failed || got.Error != "intaketest: no readiness scripted" || len(e.starter.Calls()) != 0 {
		t.Fatalf("drove to %+v", got)
	}
}

func testFirstErrorFails(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, ready)
	e.s.Firsts = FirstsFunc(func(context.Context, unit.Unit) (bool, error) { return false, errors.New("the book is unreadable") })
	i := e.accept(t, intake.Request{})
	got, err := e.s.Drive(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Failed || got.Error != "the book is unreadable" {
		t.Fatalf("drove to %+v", got)
	}
	if evs := e.store.Events(); evs[len(evs)-1].Kind != intake.KindFailed {
		t.Fatalf("events %v", kinds(evs))
	}
}

func testStoreErrorsAreReturned(t *testing.T, mk MakeStore) {
	boom := errors.New("disk full")
	for _, tc := range []struct {
		name   string
		op     string
		state  intake.State
		starts int
	}{
		{name: "accept", op: "accept", state: intake.Accepted},
		{name: "briefing", op: "save", state: intake.Briefing},
		{name: "starting", op: "save", state: intake.Starting},
		{name: "started", op: "save", state: intake.Started, starts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, mk, false, ready)
			if tc.state == intake.Briefing {
				e = newEnv(t, mk, true, briefed)
			}
			faulty, ok := e.store.(interface {
				FailWith(func(op string, i intake.Intake) error)
			})
			if !ok {
				t.Skip("the store cannot inject faults")
			}
			faulty.FailWith(func(op string, i intake.Intake) error {
				if op == tc.op && i.State == tc.state {
					return boom
				}
				return nil
			})
			i, _, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli"})
			if tc.op == "accept" {
				if !errors.Is(err, boom) {
					t.Fatalf("Accept: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.s.Drive(context.Background(), i.Seq); !errors.Is(err, boom) {
				t.Fatalf("Drive: %v", err)
			}
			if n := len(e.starter.Calls()); n != tc.starts {
				t.Fatalf("started %d turns, want %d", n, tc.starts)
			}
		})
	}
}

func testJoinOnAStartingIntakeChangesNothing(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, ready)
	i := e.store.Put(intake.Intake{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", Request: []byte("guidance:first"), State: intake.Starting, AcceptedAt: t0, StartingAt: t0})
	got, joined, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", Guidance: "second", NoBrief: true})
	if err != nil || !joined || got.Seq != i.Seq || got.State != intake.Starting {
		t.Fatalf("join: %+v %v %v", got, joined, err)
	}
	if s := e.stored(t, i.Seq); string(s.Request) != "guidance:first" || s.Brief != "" || s.Note != "" {
		t.Fatalf("stored %+v", s)
	}
	if evs := e.store.Events(); len(evs) != 0 {
		t.Fatalf("the join wrote %v", kinds(evs))
	}
}

func testJoinWithNoBriefStartsWithout(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	if _, joined, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", NoBrief: true}); err != nil || !joined {
		t.Fatalf("join: %v %v", joined, err)
	}
	got := done(t, ch)
	if got.State != intake.Started || got.Brief != intake.BriefWithout || got.Note != intake.NoteNoBrief {
		t.Fatalf("drove to %+v", got)
	}
}

func testCancelOfAStartedIntakeIsRefused(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	i := e.accept(t, intake.Request{})
	if _, err := e.s.Drive(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Cancel(context.Background(), i.Seq); !errors.Is(err, intake.ErrNotWaiting) {
		t.Fatalf("Cancel: %v", err)
	}
	if s := e.stored(t, i.Seq); s.State != intake.Started {
		t.Fatalf("stored %+v", s)
	}
}

func testASecondDriveIsRefused(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed, ready)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	if _, err := e.s.Drive(context.Background(), i.Seq); !errors.Is(err, intake.ErrDriven) {
		t.Fatalf("second Drive: %v", err)
	}
	if err := e.s.Cancel(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
	if got := done(t, ch); got.State != intake.Canceled {
		t.Fatalf("drove to %+v", got)
	}
	if len(e.starter.Calls()) != 0 {
		t.Fatal("a canceled intake started")
	}
}

func testPanickingStarterReleasesTheLoopMutex(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	e.starter.Fn = func(context.Context, intake.Intake, io.Writer) (intake.StartedTurn, error) { panic("boom") }
	i := e.accept(t, intake.Request{})
	func() {
		defer func() { _ = recover() }()
		_, _ = e.s.Drive(context.Background(), i.Seq)
	}()
	if !e.lock.TryLock() {
		t.Fatal("the loop mutex is still held after a panic")
	}
}

func testWaitLogsTheLastFailure(t *testing.T, mk MakeStore) {
	retry := intake.Readiness{Wait: "retry", Attempt: 2, LastError: "timeout", Next: t0.Add(4 * time.Minute)}
	e := newEnv(t, mk, true, retry, ready)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	want := []string{"brief attempt 1 failed: timeout; next attempt at 13:04"}
	if s := e.stored(t, i.Seq); !slices.Equal(s.Log, want) || s.Note != want[0] {
		t.Fatalf("stored log %q note %q, want %q", s.Log, s.Note, want)
	}
	eventually(t, "no timer was asked", func() bool { return len(e.timers.Asked()) > 0 })
	e.timers.Fire()
	done(t, ch)
}

func testLogLinesAreStoredWhileStarting(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	e.starter.Fn = func(_ context.Context, i intake.Intake, out io.Writer) (intake.StartedTurn, error) {
		fmt.Fprintln(out, "sending the review")
		if s := e.stored(t, i.Seq); s.State != intake.Starting || !slices.Equal(s.Log, []string{"sending the review"}) {
			t.Errorf("while starting, stored %+v", s)
		}
		return intake.StartedTurn{TurnSeq: 2, TurnID: "r-2"}, nil
	}
	i := e.accept(t, intake.Request{})
	if _, err := e.s.Drive(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
}

func testAWaitThatDidNotChangeIsNotLoggedAgain(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed, briefed, ready)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	if _, _, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", Guidance: "more"}); err != nil {
		t.Fatal(err)
	}
	e.briefs.End(r7)
	eventually(t, "Ready was not asked again", func() bool { return len(e.briefs.Asked()) == 2 })
	e.briefs.End(r7)
	got := done(t, ch)
	if want := []string{"briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)", intake.NoteGuidance}; !slices.Equal(got.Log, want) {
		t.Fatalf("log %q, want %q", got.Log, want)
	}
}

func testAResumedWaitIsNotLoggedAgain(t *testing.T, mk MakeStore) {
	const line = "briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)"
	e := newEnv(t, mk, true, briefed, ready)
	i := e.store.Put(intake.Intake{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", State: intake.Briefing, AcceptedAt: t0, BriefingAt: t0, Note: line, Log: []string{line}})
	ch := e.drive(i.Seq)
	eventually(t, "Ready was never asked", func() bool { return len(e.briefs.Asked()) == 1 })
	e.briefs.End(r7)
	if got := done(t, ch); !slices.Equal(got.Log, []string{line}) {
		t.Fatalf("log %q", got.Log)
	}
}

func testBriefsOffStartsWithOff(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, intake.Readiness{Go: true, Without: true, Cause: intake.CauseOff, Note: "briefs are off"})
	i := e.accept(t, intake.Request{})
	got, err := e.s.Drive(context.Background(), i.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != intake.Started || got.Brief != intake.BriefOff || got.Note != "briefs are off" {
		t.Fatalf("drove to %+v", got)
	}
}

func testTimesAreInTheClocksZone(t *testing.T, mk MakeStore) {
	retry := intake.Readiness{Wait: "retry", Attempt: 2, LastError: "timeout", Next: t0.Add(4 * time.Minute)}
	e := newEnv(t, mk, true, retry, ready)
	e.s.Clock = NewClock(t0.In(time.FixedZone("BRT", -3*60*60)))
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	if s := e.stored(t, i.Seq); !slices.Equal(s.Log, []string{"brief attempt 1 failed: timeout; next attempt at 10:04"}) {
		t.Fatalf("log %q, want the hour of the clock's zone", s.Log)
	}
	eventually(t, "no timer was asked", func() bool { return len(e.timers.Asked()) > 0 })
	e.timers.Fire()
	done(t, ch)
}

func testAFailedBriefingWaitNamesTheFailure(t *testing.T, mk MakeStore) {
	again := intake.Readiness{Wait: "briefing", Attempt: 2, LastError: "rate limited"}
	e := newEnv(t, mk, true, again, ready)
	i := e.accept(t, intake.Request{})
	ch := e.drive(i.Seq)
	e.waiting(t, i.Seq)
	want := []string{"brief attempt 1 failed: rate limited", "briefing acme/api#7 (attempt 2; briefs take 10 to 20 s)"}
	if s := e.stored(t, i.Seq); !slices.Equal(s.Log, want) {
		t.Fatalf("log %q, want %q", s.Log, want)
	}
	e.briefs.End(r7)
	done(t, ch)
}

func testCanJoinRefusesAJoin(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{Payload: []byte("guidance:first")})
	refused := errors.New("another source")
	got, joined, err := e.s.Accept(context.Background(), intake.Request{Unit: r7, Intent: turn.Intent(dintent.Review), Source: "cli", Guidance: "second",
		CanJoin: func(open intake.Intake) error {
			if open.Seq != i.Seq {
				t.Errorf("asked about intake %d", open.Seq)
			}
			return refused
		}})
	if !errors.Is(err, refused) || !joined || got.Seq != i.Seq {
		t.Fatalf("got %+v joined %v err %v", got, joined, err)
	}
	if s := e.stored(t, i.Seq); string(s.Request) != "guidance:first" || len(s.Log) != 0 {
		t.Fatalf("a refused join changed the intake: %+v", s)
	}
}

func testCancelForKeepsItsNote(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, true, briefed)
	i := e.accept(t, intake.Request{})
	if err := e.s.CancelFor(context.Background(), i.Seq, "the session stopped asking"); err != nil {
		t.Fatal(err)
	}
	s := e.stored(t, i.Seq)
	if s.State != intake.Canceled || s.Note != "the session stopped asking" {
		t.Fatalf("stored %+v", s)
	}
	if evs := e.store.Events(); evs[len(evs)-1].Kind != intake.KindCanceled || evs[len(evs)-1].Data != s.Note {
		t.Fatalf("last event %+v", evs[len(evs)-1])
	}
}

func testAStopWhileWaitingForTheLoopLeavesItStarting(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	e.lock.Lock()
	i := e.accept(t, intake.Request{})
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() {
		_, err := e.s.Drive(ctx, i.Seq)
		ch <- err
	}()
	eventually(t, "the intake never started", func() bool { return e.stored(t, i.Seq).State == intake.Starting })
	cancel()
	select {
	case err := <-ch:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("drive: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the drive waited for the loop mutex past its context")
	}
	e.lock.Unlock()
	if s := e.stored(t, i.Seq); s.State != intake.Starting || len(e.starter.Calls()) != 0 {
		t.Fatalf("stored %+v, %d starts", s, len(e.starter.Calls()))
	}
	if _, err := e.s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := e.stored(t, i.Seq); s.State != intake.Failed || s.Error != intake.NoteStopped {
		t.Fatalf("after Recover %+v", s)
	}
}

type aheadKey struct{}

// aheadStarter is a Starter that reads ahead, noting whether the loop mutex
// was free then.
type aheadStarter struct {
	*Starter
	lock *sync.Mutex
	free []bool
}

func (a *aheadStarter) ReadAhead(ctx context.Context, i intake.Intake) context.Context {
	free := a.lock.TryLock()
	if free {
		a.lock.Unlock()
	}
	a.free = append(a.free, free)
	return context.WithValue(ctx, aheadKey{}, i.Unit)
}

func testAStarterReadsAheadBeforeTheLoopMutex(t *testing.T, mk MakeStore) {
	e := newEnv(t, mk, false, ready)
	a := &aheadStarter{Starter: e.starter, lock: e.lock}
	e.s.Starter = a
	var got any
	e.starter.Fn = func(ctx context.Context, i intake.Intake, _ io.Writer) (intake.StartedTurn, error) {
		got = ctx.Value(aheadKey{})
		return intake.StartedTurn{TurnSeq: 2, TurnID: "r-2"}, nil
	}
	i := e.accept(t, intake.Request{})
	if _, err := e.s.Drive(context.Background(), i.Seq); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.free, []bool{true}) || got != r7 {
		t.Fatalf("read ahead with the mutex free %v; the start saw %v", a.free, got)
	}
}
