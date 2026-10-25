package automation_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var t0 = time.Date(2026, 10, 21, 10, 0, 0, 0, time.FixedZone("BRT", -3*60*60))

var reach = map[string]workflow.Reach{
	"session.close": workflow.ReachLocal, "session.mark_closed": workflow.ReachLocal, "workspace.remove": workflow.ReachLocal,
	"turn.start": workflow.ReachLocal, "classify": workflow.ReachLocal, "notify.self": workflow.ReachPrivate,
	"label.add": workflow.ReachVisible,
}

var (
	s1 = unit.Unit{Provider: unit.Session, Key: "t1"}
	s2 = unit.Unit{Provider: unit.Session, Key: "t2"}
	s3 = unit.Unit{Provider: unit.Session, Key: "t3"}
	p1 = unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}
	p2 = unit.Unit{Provider: unit.GitHub, Key: "o/r#2"}
	w1 = unit.Unit{Provider: unit.Workspace, Key: "wt1"}
)

var always = automationtest.MatchFunc(func(workflow.Vars) bool { return true })

var idle = automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Facts["session.idle"] == true }, "session.idle")

type rig struct {
	t        *testing.T
	clock    *automationtest.Clock
	log      *change.Log
	units    *automationtest.Units
	events   *automationtest.Events
	steps    *automationtest.Steps
	ledger   *automationtest.Ledger
	guidance *automationtest.Guidance
	state    *stateLoader
	loop     *recLock
	pause    automation.Pause
	reach    error
	wfs      []workflow.Workflow
	e        *automation.Engine
	trace    []string
}

func newRig(t *testing.T, wfs ...workflow.Workflow) *rig {
	t.Helper()
	r := &rig{t: t, clock: automationtest.NewClock(t0), log: change.NewLog(256, nil), state: &stateLoader{}, wfs: wfs}
	r.loop = &recLock{r: r}
	tr := automationtest.Trace(func(call string) { r.trace = append(r.trace, r.loop.tag()+call) })
	r.units = automationtest.NewUnits(tr)
	r.events = automationtest.NewEvents(tr)
	r.steps = automationtest.NewSteps(reach, tr)
	r.ledger = automationtest.NewLedger(tr, automationtest.Notes(r.log))
	r.guidance = automationtest.NewGuidance(tr)
	r.restart()
	return r
}

// restart builds a new engine over the same stores, as after a daemon restart.
func (r *rig) restart() {
	r.e = &automation.Engine{
		Workflows: func() []workflow.Workflow { return r.wfs },
		Units:     r.units, Events: r.events, Steps: r.steps, Ledger: r.ledger, Guidance: r.guidance,
		Changes: r.log, Clock: r.clock, State: r.state, RetryBase: time.Minute,
		Pause:     func(context.Context) (automation.Pause, error) { return r.pause, nil },
		Config:    func() workflow.Facts { return workflow.Facts{"config.cleanup.idle": 2 * time.Hour} },
		Reachable: func(context.Context) error { return r.reach },
		Log:       r.t.Logf,
	}
}

func (r *rig) strict() {
	r.units.Strict(r.t)
	r.events.Strict(r.t)
	r.steps.Strict(r.t)
	r.ledger.Strict(r.t)
	r.guidance.Strict(r.t)
}

func (r *rig) put(u unit.Unit, fp string, extra ...any) {
	slot := map[unit.ProviderID]string{unit.GitHub: "pr", unit.Session: "session", unit.Workspace: "workspace"}[u.Provider]
	f := workflow.Facts{slot + ".fingerprint": fp, slot + ".repo": "o/r", slot + ".idle": true}
	for i := 0; i+1 < len(extra); i += 2 {
		f[extra[i].(string)] = extra[i+1]
	}
	r.units.Put(u, f)
}

func (r *rig) sweep() string {
	r.t.Helper()
	sum, skipped, err := r.e.Sweep(context.Background(), r.loop)
	if err != nil {
		r.t.Fatalf("Sweep: %v", err)
	}
	if skipped != "" {
		r.t.Fatalf("Sweep skipped: %s", skipped)
	}
	return sum
}

// sweepMayFail sweeps where a failed step may fail the sweep.
func (r *rig) sweepMayFail() {
	r.t.Helper()
	if _, _, err := r.e.Sweep(context.Background(), r.loop); err != nil && !strings.Contains(err.Error(), "failed") {
		r.t.Fatalf("Sweep: %v", err)
	}
}

func (r *rig) evaluate() automation.Plan {
	r.t.Helper()
	p, err := r.e.Evaluate(context.Background(), "")
	if err != nil {
		r.t.Fatalf("Evaluate: %v", err)
	}
	return p
}

func (r *rig) items() []workflow.Item {
	r.t.Helper()
	its, err := r.ledger.Items(context.Background(), automation.Filter{})
	if err != nil {
		r.t.Fatal(err)
	}
	return its
}

func (r *rig) item(wf string, u unit.Unit) (workflow.Item, bool) {
	for _, it := range r.items() {
		if it.Workflow == wf && it.Unit == u {
			return it, true
		}
	}
	return workflow.Item{}, false
}

func (r *rig) mustItem(wf string, u unit.Unit) workflow.Item {
	r.t.Helper()
	it, ok := r.item(wf, u)
	if !ok {
		r.t.Fatalf("no item of %s on %s in %+v", wf, u, r.items())
	}
	return it
}

func (r *rig) ran(use string) []unit.Unit {
	var out []unit.Unit
	for _, c := range r.steps.Ran() {
		if c.Step.Use == use {
			out = append(out, c.Unit)
		}
	}
	return out
}

func planItem(p automation.Plan, wf string, u unit.Unit) (workflow.Item, bool) {
	for _, it := range p.Items {
		if it.Workflow == wf && it.Unit == u {
			return it, true
		}
	}
	return workflow.Item{}, false
}

type recLock struct {
	r    *rig
	mu   sync.Mutex
	held bool
}

func (l *recLock) Lock()   { l.mu.Lock(); l.held = true }
func (l *recLock) Unlock() { l.held = false; l.mu.Unlock() }
func (l *recLock) tag() string {
	if l.held {
		return "locked:"
	}
	return ""
}

type stateLoader struct{ loads, saves int }

type state struct{ l *stateLoader }

func (s state) Save(context.Context) error { s.l.saves++; return nil }

func (l *stateLoader) Load(context.Context) (automation.State, error) {
	l.loads++
	return state{l}, nil
}

func sweepWF(name string, when workflow.Predicate, steps ...string) workflow.Workflow {
	w := workflow.Workflow{Name: name, Provider: unit.Session, Slot: "session", Trigger: workflow.Trigger{Kind: workflow.TriggerSweep},
		When: when, Mode: workflow.ModeApply}
	for _, s := range steps {
		w.Steps = append(w.Steps, workflow.Step{Use: s, Reach: reach[s]})
	}
	return w
}

func eventWF(name string, p unit.ProviderID, slot string, when workflow.Predicate, on ...string) workflow.Workflow {
	w := sweepWF(name, when, "session.close")
	w.Provider, w.Slot, w.Trigger = p, slot, workflow.Trigger{Kind: workflow.TriggerEvent, On: on}
	return w
}

func scheduleWF(t *testing.T, name, spec string) workflow.Workflow {
	c, err := workflow.ParseCron(spec)
	if err != nil {
		t.Fatal(err)
	}
	w := sweepWF(name, always, "notify.self")
	w.Trigger = workflow.Trigger{Kind: workflow.TriggerSchedule, Schedule: c}
	return w
}

func windowed(w workflow.Workflow, d time.Duration) workflow.Workflow {
	w.Approval = workflow.Approval{Default: workflow.OutcomeRun, Window: d}
	return w
}

func TestEvaluateWritesNothing(t *testing.T) {
	ctx := context.Background()
	closeW := windowed(sweepWF("close", idle, "session.close"), 5*time.Minute)
	guided := windowed(sweepWF("start", always, "turn.start"), 5*time.Minute)
	guided.Provider, guided.Slot = unit.GitHub, "pr"
	capped := sweepWF("capped", idle, "session.mark_closed")
	capped.Limits.PerDay = 1
	blocked := sweepWF("blocked", idle, "notify.self")
	merged := eventWF("merged", unit.GitHub, "pr", always, "merged")
	merged.Steps = []workflow.Step{{Use: "turn.start"}}
	daily := scheduleWF(t, "daily", "0 9 * * *")
	fresh := eventWF("fresh", unit.Session, "session", always, "closed")
	freshDaily := scheduleWF(t, "fresh-daily", "0 9 * * *")
	r := newRig(t, closeW, guided, capped, blocked, merged, daily, fresh, freshDaily)
	r.put(s1, "a")
	r.put(p1, "h1")
	r.guidance.Set(p1, "look at the tests")
	r.steps.Refuse("notify.self", s1, "not today")
	done := workflow.NewKey(capped, s2, workflow.Facts{"session.fingerprint": "old"}, time.Time{})
	if err := r.ledger.Record(ctx, workflow.Run{Key: done, Status: workflow.RunDone, At: t0.Add(-time.Hour)}, workflow.Item{ID: done.ID()}); err != nil {
		t.Fatal(err)
	}
	if err := r.events.Advance(ctx, "merged", 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := r.events.Advance(ctx, "daily", 0, t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.events.Append(workflow.Event{Unit: p1, Kind: "merged", At: t0})
	r.strict()
	gen := r.log.Gen()

	p := r.evaluate()

	if r.log.Gen() != gen {
		t.Errorf("Evaluate noted the change log: %+v", r.log.Since(gen, ^port.Topic(0)))
	}
	for wf, want := range map[string]workflow.Decision{"close": workflow.DecisionPending, "start": workflow.DecisionRun,
		"capped": workflow.DecisionSkipped, "blocked": workflow.DecisionBlocked} {
		u := s1
		if wf == "start" {
			u = p1
		}
		if it, ok := planItem(p, wf, u); !ok || it.Decision != want {
			t.Errorf("%s: %+v, want %s", wf, it, want)
		}
	}
	if it, ok := planItem(p, "merged", p1); !ok || it.Why.Event == nil {
		t.Errorf("the merged event planned %+v", it)
	}
	if it, ok := planItem(p, "daily", s1); !ok || !it.Why.FiredAt.Equal(time.Date(2026, 10, 21, 9, 0, 0, 0, t0.Location())) {
		t.Errorf("the 9:00 firing planned %+v", it)
	}
	if p.Gen != gen {
		t.Errorf("Plan.Gen = %d, want %d", p.Gen, gen)
	}
	for _, c := range r.trace {
		if c == "units.refresh" || c == "units.settle" || c == "units.moved" {
			t.Errorf("Evaluate called %s", c)
		}
	}
}

func TestAMatchingUnitRunsOncePerKey(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.put(s2, "b", "session.idle", false)
	r.sweep()
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s1}) {
		t.Fatalf("ran on %v, want s1 once", got)
	}
	r.put(s1, "a2")
	r.sweep()
	if got := r.ran("session.close"); len(got) != 2 {
		t.Errorf("a new fingerprint ran %v, want a second run", got)
	}
}

func TestSkipIsTerminalForTheKey(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	it := r.mustItem("close", s1)
	if _, err := r.e.Act(context.Background(), it.ID, workflow.ActionSkip, ""); err != nil {
		t.Fatal(err)
	}
	if d := r.steps.Dismissed(); len(d) != 1 || d[0].Unit != s1 {
		t.Errorf("Dismissed = %+v", d)
	}
	r.clock.Advance(10 * time.Minute)
	r.sweep()
	if _, ok := r.item("close", s1); ok || len(r.steps.Ran()) != 0 {
		t.Errorf("a skipped key came back: %+v, ran %+v", r.items(), r.steps.Ran())
	}
}

func TestDropDiscardsWithoutMarking(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	if err := r.e.Drop(ctx, s1, "held by you"); err != nil {
		t.Fatal(err)
	}
	if len(r.items()) != 0 {
		t.Fatalf("Drop left %+v", r.items())
	}
	if !slices.ContainsFunc(r.ledger.Events(), func(ev port.Event) bool {
		return ev.Kind == workflow.KindItemDropped && ev.Data == "held by you"
	}) {
		t.Error("no workflow_dropped event")
	}
	r.sweep()
	if it := r.mustItem("close", s1); it.Decision != workflow.DecisionPending {
		t.Errorf("after a drop the item is %s, want pending again", it.Decision)
	}
}

func TestAGuardRefusalIsReevaluatedAndRunsWhenItPasses(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.steps.Refuse("session.close", s1, "the agent is not done")
	r.sweep()
	it := r.mustItem("close", s1)
	if it.Decision != workflow.DecisionBlocked || it.Reason != "the agent is not done" || it.Attempts != 0 {
		t.Fatalf("item = %+v", it)
	}
	if n := r.steps.Noted(); len(n) != 1 || n[0].Refusal != "the agent is not done" {
		t.Errorf("Noted = %+v", n)
	}
	r.steps.Refuse("session.close", s1, "")
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("ran %v after the guard passed", got)
	}
}

func TestACheckErrorBlocksWithoutConsumingAttempts(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.steps.CheckErr("session.close", s1, errors.New("registry unreadable"))
	r.sweep()
	it := r.mustItem("close", s1)
	if it.Decision != workflow.DecisionBlocked || it.Reason != "registry unreadable" || it.Attempts != 0 || len(r.steps.Ran()) != 0 {
		t.Errorf("item = %+v, ran %+v", it, r.steps.Ran())
	}
	if _, ok, _ := r.ledger.Run(context.Background(), it.Key); ok {
		t.Error("a check error recorded a run")
	}
}

func TestAFailedStepRetriesThreeTimesThenBlocks(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.steps.Fail("session.close", s1, errors.New("orca said no"))
	r.sweepMayFail()
	it := r.mustItem("close", s1)
	if it.Decision != workflow.DecisionSkipped || it.Reason != "retrying after orca said no" || it.Attempts != 1 {
		t.Fatalf("after one failure: %+v", it)
	}
	r.sweepMayFail()
	if it := r.mustItem("close", s1); it.Attempts != 1 {
		t.Fatalf("retried before the backoff: %+v", it)
	}
	r.clock.Advance(2 * time.Minute)
	r.sweepMayFail()
	r.clock.Advance(4 * time.Minute)
	r.sweepMayFail()
	it = r.mustItem("close", s1)
	if it.Decision != workflow.DecisionBlocked || it.Attempts != workflow.MaxAttempts || !strings.Contains(it.Reason, "failed 3 times: orca said no") {
		t.Fatalf("after three failures: %+v", it)
	}
	r.clock.Advance(time.Hour)
	r.sweepMayFail()
	if it := r.mustItem("close", s1); it.Decision != workflow.DecisionBlocked || it.Attempts != 3 {
		t.Errorf("a blocked key ran again: %+v", it)
	}
}

func TestATransientErrorIsNotAnAttempt(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.steps.Fail("session.close", s1, automation.Transient{Err: errors.New("orca unreachable")})
	r.sweep()
	it := r.mustItem("close", s1)
	if _, ok, _ := r.ledger.Run(context.Background(), it.Key); ok || it.Attempts != 0 {
		t.Fatalf("a transient failure recorded %+v", it)
	}
	r.steps.Fail("session.close", s1, nil)
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("ran %v on the next sweep", got)
	}
}

func TestApplyOnABlockedItemResetsAttempts(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.steps.Fail("session.close", s1, errors.New("boom"))
	for range workflow.MaxAttempts {
		r.sweepMayFail()
		r.clock.Advance(time.Hour)
	}
	it := r.mustItem("close", s1)
	if it.Decision != workflow.DecisionBlocked {
		t.Fatalf("item = %+v", it)
	}
	got, err := r.e.Act(context.Background(), it.ID, workflow.ActionApply, "")
	if err == nil || got.Attempts != 1 || got.Decision != workflow.DecisionSkipped {
		t.Fatalf("apply on a still failing step = %+v, %v; want one attempt", got, err)
	}
	r.steps.Fail("session.close", s1, nil)
	if _, err := r.e.Act(context.Background(), it.ID, workflow.ActionApply, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.item("close", s1); ok || len(r.ran("session.close")) != 1 {
		t.Errorf("apply did not run: %+v", r.items())
	}
}

func TestApplyByHandIsConfirmed(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	if _, err := r.e.Act(context.Background(), r.mustItem("close", s1).ID, workflow.ActionApply, ""); err != nil {
		t.Fatal(err)
	}
	ran := r.steps.Ran()
	if len(ran) != 1 || !ran[0].Run.ByHand || !ran[0].Run.Confirmed {
		t.Errorf("Ran = %+v", ran)
	}
	if r.state.loads != 2 || r.state.saves != 2 {
		t.Errorf("state loads %d, saves %d; want one each for the sweep and the action", r.state.loads, r.state.saves)
	}
}

func TestADeferredStepStaysPendingUntilItsResultIsStored(t *testing.T) {
	w := eventWF("triage", unit.GitHub, "pr", always, "review")
	w.Steps = []workflow.Step{{Use: "classify", Reach: workflow.ReachLocal}}
	r := newRig(t, w)
	r.put(p1, "h1")
	r.sweep()
	r.events.Append(workflow.Event{Unit: p1, Kind: "review", At: t0})
	r.steps.Defer("classify", p1, "classifying")
	r.sweep()
	it := r.mustItem("triage", p1)
	if it.Decision != workflow.DecisionPending || it.Reason != "classifying" {
		t.Fatalf("a deferred item is %+v", it)
	}
	gen := r.log.Gen()
	r.sweep()
	if c := r.log.Since(gen, port.SweepInputs); !c.Empty() {
		t.Errorf("a deferred item waiting for its result woke the sweep: %+v", c)
	}
	r.clock.Advance(time.Minute)
	r.sweep()
	if it := r.mustItem("triage", p1); it.Decision != workflow.DecisionPending {
		t.Fatalf("still classifying, the item is %+v", it)
	}
	r.steps.Defer("classify", p1, "")
	r.clock.Advance(time.Minute)
	r.sweep()
	if _, ok := r.item("triage", p1); ok || len(r.ran("classify")) != 1 {
		t.Errorf("the stored result did not finish the item: %+v", r.items())
	}
}

func TestStepsRunInOrderAndStopAtTheFirstFailure(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.mark_closed", "session.close", "notify.self"))
	r.put(s1, "a")
	r.steps.Fail("session.close", s1, errors.New("boom"))
	r.sweepMayFail()
	var uses []string
	for _, c := range r.steps.Ran() {
		uses = append(uses, c.Step.Use)
	}
	if !slices.Equal(uses, []string{"session.mark_closed"}) {
		t.Fatalf("ran %v", uses)
	}
	r.steps.Fail("session.close", s1, nil)
	r.clock.Advance(time.Hour)
	r.sweepMayFail()
	uses = nil
	for _, c := range r.steps.Ran() {
		uses = append(uses, c.Step.Use)
	}
	if !slices.Equal(uses, []string{"session.mark_closed", "session.mark_closed", "session.close", "notify.self"}) {
		t.Errorf("ran %v", uses)
	}
}

func TestObserveOnlyPlans(t *testing.T) {
	w := sweepWF("close", idle, "session.close")
	w.Mode = workflow.ModeObserve
	r := newRig(t, w)
	r.put(s1, "a")
	r.put(s2, "b")
	r.steps.Refuse("session.close", s2, "busy")
	r.sweep()
	r.clock.Advance(time.Hour)
	r.sweep()
	if it := r.mustItem("close", s1); it.Decision != workflow.DecisionPending || it.Default != workflow.OutcomeWait || it.Reason != "observe mode" {
		t.Errorf("item = %+v", it)
	}
	if len(r.steps.Ran()) != 0 || len(r.steps.Noted()) != 0 {
		t.Errorf("observe ran %+v, noted %+v", r.steps.Ran(), r.steps.Noted())
	}
}

func TestWaitWhenMakesAPendingItemEvenWhenAGuardRefuses(t *testing.T) {
	w := sweepWF("finished", idle, "workspace.remove")
	w.Approval.WaitWhen = automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Facts["session.dirty"] == true }, "session.dirty")
	r := newRig(t, w)
	r.put(s1, "a", "session.dirty", true)
	r.put(s2, "b")
	r.put(s3, "c", "session.dirty", true)
	r.steps.CheckErr("workspace.remove", s3, errors.New("git status failed"))
	refusal := "unpushed commits; back up with: git push origin HEAD:refs/heads/backup"
	r.steps.Refuse("workspace.remove", s1, refusal)
	r.steps.Refuse("workspace.remove", s2, refusal)
	r.sweep()
	if it := r.mustItem("finished", s1); it.Decision != workflow.DecisionPending || it.Default != workflow.OutcomeWait || it.Reason != "session.dirty true: "+refusal {
		t.Errorf("wait_when item = %+v", it)
	}
	if it := r.mustItem("finished", s2); it.Decision != workflow.DecisionBlocked {
		t.Errorf("without wait_when = %+v", it)
	}
	if it := r.mustItem("finished", s3); it.Decision != workflow.DecisionBlocked || it.Reason != "git status failed" {
		t.Errorf("wait_when over a check error = %+v; want blocked", it)
	}
	_, err := r.e.Act(context.Background(), r.mustItem("finished", s1).ID, workflow.ActionApply, "")
	if !errors.As(err, new(automation.Refused)) || len(r.steps.Ran()) != 0 {
		t.Errorf("applying it kept no guard: %v, ran %+v", err, r.steps.Ran())
	}
}

func TestAWaitNamesWhatItWaitsOn(t *testing.T) {
	w := sweepWF("finished", idle, "workspace.remove")
	w.Approval.WaitWhen = automationtest.MatchFunc(func(v workflow.Vars) bool {
		p, _ := v.Facts["session.pendency"].([]string)
		return len(p) > 0
	}, "session.dirty", "session.pendency")
	r := newRig(t, w)
	r.put(s1, "a", "session.pendency", []string{"turn asked unanswered"}, "session.dirty", 0)
	r.put(s2, "b", "session.pendency", []string{"modified files", "commits on no remote"}, "session.dirty", 1)
	refusal := "3 commits not pushed in /w; back them up with: git push origin HEAD:refs/heads/me/backup-b"
	r.steps.Refuse("workspace.remove", s2, refusal)
	r.sweep()
	if it := r.mustItem("finished", s1); it.Reason != "session.pendency [turn asked unanswered]" {
		t.Errorf("a wait with no refusal: %q", it.Reason)
	}
	if it := r.mustItem("finished", s2); it.Reason != "session.pendency [modified files, commits on no remote], session.dirty 1: "+refusal {
		t.Errorf("a wait over a refusal: %q", it.Reason)
	}
}

func TestAVisibleStepWaitsUnlessAllowed(t *testing.T) {
	w := sweepWF("label", idle, "label.add")
	allowed := sweepWF("label-ok", idle, "label.add")
	allowed.AllowVisible = true
	r := newRig(t, w, allowed)
	r.put(s1, "a")
	r.sweep()
	if it := r.mustItem("label", s1); it.Decision != workflow.DecisionPending || it.Default != workflow.OutcomeWait {
		t.Errorf("a visible step = %+v", it)
	}
	if got := r.ran("label.add"); !slices.Equal(got, []unit.Unit{s1}) {
		t.Errorf("ran %v; want only the allowed workflow", got)
	}
}

func TestApplyByHandLiftsOnlyTheVisibleDefault(t *testing.T) {
	r := newRig(t, sweepWF("label", idle, "label.add"))
	r.put(s1, "a")
	r.put(s2, "b")
	r.sweep()
	it2 := r.mustItem("label", s2)
	r.steps.Refuse("label.add", s2, "not yours to label")
	if _, err := r.e.Act(context.Background(), r.mustItem("label", s1).ID, workflow.ActionApply, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.e.Act(context.Background(), it2.ID, workflow.ActionApply, ""); !errors.As(err, new(automation.Refused)) {
		t.Errorf("a guard refusal by hand = %v", err)
	}
	if got := r.ran("label.add"); !slices.Equal(got, []unit.Unit{s1}) {
		t.Errorf("ran %v", got)
	}
	if it := r.mustItem("label", s2); it.Decision != workflow.DecisionBlocked || it.Reason != "not yours to label" {
		t.Errorf("the refused item = %+v", it)
	}
}

func TestAPendingWindowRunsWhenItEnds(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	it := r.mustItem("close", s1)
	if it.Decision != workflow.DecisionPending || it.Default != workflow.OutcomeRun || !it.Deadline.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("item = %+v", it)
	}
	r.clock.Advance(2 * time.Minute)
	r.sweep()
	if it := r.mustItem("close", s1); !it.Deadline.Equal(t0.Add(5*time.Minute)) || len(r.steps.Ran()) != 0 {
		t.Fatalf("the window moved or ran early: %+v", it)
	}
	r.clock.Advance(3 * time.Minute)
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("ran %v at the window's end", got)
	}
}

func TestGuideRunsAtOnceWithTheGuidance(t *testing.T) {
	w := windowed(sweepWF("review", always, "turn.start"), 5*time.Minute)
	w.Provider, w.Slot = unit.GitHub, "pr"
	r := newRig(t, w)
	r.put(p1, "h1")
	r.sweep()
	if _, err := r.e.Act(context.Background(), r.mustItem("review", p1).ID, workflow.ActionGuide, "focus on the tests"); err != nil {
		t.Fatal(err)
	}
	ran := r.steps.Ran()
	if len(ran) != 1 || ran[0].Run.Guidance != "focus on the tests" || !ran[0].Run.Confirmed {
		t.Errorf("Ran = %+v", ran)
	}
}

func TestAPendingItemPastItsWindowRunsOnceAfterARestart(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	r.restart()
	r.clock.Advance(time.Hour)
	r.sweep()
	r.sweep()
	r.restart()
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("ran %v after the restart, want once", got)
	}
}

func TestPeekedGuidanceRunsAndIsTakenOnlyInRun(t *testing.T) {
	w := windowed(sweepWF("review", always, "turn.start"), 5*time.Minute)
	w.Provider, w.Slot = unit.GitHub, "pr"
	tag := sweepWF("tag", always, "notify.self")
	tag.Provider, tag.Slot, tag.Order = unit.GitHub, "pr", -1
	r := newRig(t, w, tag)
	r.put(p1, "h1")
	r.put(p2, "h2")
	r.guidance.Set(p1, "check the migration")
	if it, _ := planItem(r.evaluate(), "review", p1); it.Decision != workflow.DecisionRun || it.Reason != "guided by you" {
		t.Fatalf("a guided item = %+v", it)
	}
	if len(r.guidance.Taken()) != 0 {
		t.Fatal("Evaluate took the guidance")
	}
	r.sweep()
	var ran []automationtest.Call
	for _, c := range r.steps.Ran() {
		if c.Step.Use == "turn.start" {
			ran = append(ran, c)
		}
	}
	if len(ran) != 1 || ran[0].Run.Guidance != "check the migration" || !slices.Equal(r.guidance.Taken(), []unit.Unit{p1}) {
		t.Errorf("Ran = %+v, taken %v; only the guided item takes guidance", ran, r.guidance.Taken())
	}
	if len(r.ran("notify.self")) != 2 {
		t.Errorf("the unguided items ran %v", r.ran("notify.self"))
	}
}

func TestPendingServesTheReadModel(t *testing.T) {
	w := windowed(sweepWF("review", always, "turn.start"), 5*time.Minute)
	w.Provider, w.Slot = unit.GitHub, "pr"
	r := newRig(t, w, sweepWF("close", idle, "session.close"))
	r.put(p1, "h1")
	r.put(s1, "a")
	r.sweep()
	got, err := r.e.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Unit != p1 || got[0].Steps[0].Use != "turn.start" || !got[0].Deadline.Equal(t0.Add(5*time.Minute)) || !got[0].At.Equal(t0) {
		t.Errorf("Pending = %+v", got)
	}
}

func TestPauseStopsApplyNotObserve(t *testing.T) {
	observe := sweepWF("watch", idle, "session.close")
	observe.Mode = workflow.ModeObserve
	r := newRig(t, sweepWF("close", idle, "session.close"), observe)
	r.put(s1, "a")
	r.pause = automation.Pause{On: true}
	r.sweep()
	if it := r.mustItem("close", s1); it.Decision != workflow.DecisionSkipped || it.Reason != "paused" {
		t.Errorf("apply under pause = %+v", it)
	}
	if it := r.mustItem("watch", s1); it.Decision != workflow.DecisionPending {
		t.Errorf("observe under pause = %+v", it)
	}
	if len(r.steps.Ran()) != 0 {
		t.Errorf("ran under pause: %+v", r.steps.Ran())
	}
}

func TestPausedReposReadTheRepoFact(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a", "session.repo", "o/kept")
	r.put(s2, "b", "session.repo", "")
	r.put(s3, "c", "session.repo", "o/paused")
	r.pause = automation.Pause{Repos: []string{"O/Paused"}}
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s1, s2}) {
		t.Errorf("ran %v, want all but the paused repo", got)
	}
}

func TestLimitsSkipWithTheirReason(t *testing.T) {
	perRun := sweepWF("per-run", idle, "session.close")
	perRun.Limits.PerRun = 1
	perDay := sweepWF("per-day", idle, "session.mark_closed")
	perDay.Limits.PerDay = 2
	r := newRig(t, perRun, perDay)
	r.put(s1, "a")
	r.put(s2, "b")
	r.put(s3, "c")
	r.sweep()
	if it := r.mustItem("per-run", s2); it.Decision != workflow.DecisionSkipped || it.Reason != "limit per_run 1 reached" {
		t.Errorf("per_run = %+v", it)
	}
	if it := r.mustItem("per-day", s3); it.Decision != workflow.DecisionSkipped || it.Reason != "limit per_day 2 reached" {
		t.Errorf("per_day = %+v", it)
	}
	r.sweep()
	if it := r.mustItem("per-day", s3); it.Reason != "limit per_day 2 reached" {
		t.Errorf("per_day on the second sweep = %+v", it)
	}
	r.clock.Advance(24 * time.Hour)
	r.sweep()
	if got := r.ran("session.mark_closed"); len(got) != 3 {
		t.Errorf("the next day ran %v", got)
	}
	perUnit := sweepWF("per-unit", idle, "notify.self")
	perUnit.Limits.PerUnitPerDay = 1
	r.wfs = []workflow.Workflow{perUnit}
	r.sweep()
	r.put(s1, "a2")
	r.sweep()
	if it := r.mustItem("per-unit", s1); it.Reason != "limit per_unit_per_day 1 reached" {
		t.Errorf("per_unit_per_day = %+v", it)
	}
}

func TestCooldown(t *testing.T) {
	w := sweepWF("close", idle, "session.close")
	w.Limits.Cooldown = time.Hour
	r := newRig(t, w)
	r.put(s1, "a")
	r.sweep()
	r.put(s1, "a2")
	r.put(s2, "b")
	r.sweep()
	if it := r.mustItem("close", s1); it.Decision != workflow.DecisionSkipped || it.Reason != "cooling down for 1h0m0s" {
		t.Errorf("item = %+v", it)
	}
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s1, s2}) {
		t.Errorf("ran %v; the cooldown is per unit", got)
	}
	r.clock.Advance(61 * time.Minute)
	r.sweep()
	if got := r.ran("session.close"); len(got) != 3 {
		t.Errorf("ran %v after the cooldown", got)
	}
}

func TestOrderAcrossWorkflows(t *testing.T) {
	late, early := sweepWF("a-late", idle, "session.close"), sweepWF("z-early", idle, "session.close")
	late.Order, early.Order = 2, 1
	r := newRig(t, late, early)
	r.put(s1, "a")
	var wfs []string
	for _, it := range r.evaluate().Items {
		wfs = append(wfs, it.Workflow)
	}
	if !slices.Equal(wfs, []string{"z-early", "a-late"}) {
		t.Errorf("items in order %v", wfs)
	}
}

func TestNoteRunsOnlyUnderTheLockInApply(t *testing.T) {
	observe := sweepWF("watch", idle, "notify.self")
	observe.Mode = workflow.ModeObserve
	r := newRig(t, sweepWF("close", idle, "session.close"), observe)
	r.put(s1, "a")
	r.steps.Refuse("session.close", s1, "brief not ready")
	r.steps.Refuse("notify.self", s1, "not now")
	r.evaluate()
	if len(r.steps.Noted()) != 0 {
		t.Fatal("Evaluate noted a refusal")
	}
	r.sweep()
	if n := r.steps.Noted(); len(n) != 1 || n[0].Step.Use != "session.close" {
		t.Errorf("Noted = %+v", n)
	}
	if !slices.Contains(r.trace, "locked:steps.note") || slices.Contains(r.trace, "steps.note") {
		t.Errorf("Note ran outside the lock: %v", r.trace)
	}
}

func TestSweepPlansWithoutTheLockAndActsUnderIt(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.sweep()
	for _, c := range []string{"units.list", "units.facts", "steps.check", "ledger.save", "units.moved", "units.refresh"} {
		if !slices.Contains(r.trace, c) || slices.Contains(r.trace, "locked:"+c) {
			t.Errorf("%s ran under the lock: %v", c, r.trace)
		}
	}
	for _, c := range []string{"steps.run", "ledger.record", "units.settle"} {
		if !slices.Contains(r.trace, "locked:"+c) || slices.Contains(r.trace, c) {
			t.Errorf("%s ran outside the lock: %v", c, r.trace)
		}
	}
}

// noteOnMoved runs fn when the sweep asks Moved, after the plan is saved and
// before it takes the loop lock.
func noteOnMoved(t *testing.T, r *rig, fn func()) {
	t.Helper()
	r.units = automationtest.NewUnits(automationtest.Trace(func(c string) {
		r.trace = append(r.trace, r.loop.tag()+c)
		if c == "units.moved" {
			fn()
		}
	}))
	r.restart()
}

func TestAUnitNotedAfterThePlanIsRevalidated(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	noteOnMoved(t, r, func() {
		r.units.Set(s1, "session.idle", false)
		r.log.Note(port.TopicTurns, s1, s2)
	})
	r.put(s1, "a")
	r.put(s2, "b")
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s2}) {
		t.Errorf("ran %v; s1 stopped matching after the plan, s2 still matches", got)
	}
}

func TestAMovedSourceDropsTheRound(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.units.SetMoved(s1)
	sum := r.sweep()
	if len(r.steps.Ran()) != 0 || !strings.Contains(sum, "1 dropped") {
		t.Fatalf("a moved source ran: %s", sum)
	}
	r.units.SetMoved()
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("ran %v on the next sweep", got)
	}
}

func TestOwnWritesDoNotWakeTheSweep(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute), sweepWF("mark", idle, "session.mark_closed"))
	r.put(s1, "a")
	g0 := r.log.Gen()
	r.sweep()
	if c := r.log.Since(g0, port.SweepInputs); c.Topics != port.TopicGate {
		t.Errorf("the first pass woke the sweep with %+v; want the pending set once", c)
	}
	if c := r.log.Since(g0, ^port.Topic(0)); !c.Has(port.TopicLoopEvents) {
		t.Errorf("the engine's events noted %+v", c)
	}
	g1 := r.log.Gen()
	r.sweep()
	if r.log.Gen() != g1 {
		t.Errorf("an unchanged pass noted %+v", r.log.Since(g1, ^port.Topic(0)))
	}
}

func TestRevalidationUsesTheBasesTopicSet(t *testing.T) {
	if automation.Revalidate != port.TopicPRs|port.TopicSourceEvents|port.TopicTurns|port.TopicOverrides {
		t.Errorf("Revalidate = %b", automation.Revalidate)
	}
	r := newRig(t, sweepWF("close", idle, "session.close"))
	noteOnMoved(t, r, func() {
		r.units.Set(s1, "session.idle", false)
		r.log.Note(port.TopicSessions, s1)
	})
	r.put(s1, "a")
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("a note outside the set dropped the run: ran %v", got)
	}
}

func TestDueIsTheEarliestDeadline(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute), scheduleWF(t, "daily", "30 10 * * *"))
	r.put(s1, "a")
	r.sweepMayFail()
	if due := r.e.Due(); !due.Equal(t0.Add(5 * time.Minute)) {
		t.Errorf("Due = %v, want the window's end", due)
	}
	r.wfs = append(r.wfs, scheduleWF(t, "soon", "2 10 * * *"))
	r.sweepMayFail()
	if due := r.e.Due(); !due.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("Due = %v, want the next firing", due)
	}
	r.wfs = append(r.wfs, sweepWF("fail", idle, "notify.self"))
	r.steps.Fail("notify.self", s1, errors.New("boom"))
	r.e.RetryBase = 30 * time.Second
	r.sweepMayFail()
	r.sweepMayFail()
	if due := r.e.Due(); !due.Equal(t0.Add(time.Minute)) {
		t.Errorf("Due = %v, want the retry", due)
	}
}

func TestASessionCrossingIdleIsDueWithoutAnotherWake(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	idleSince := t0.Add(-90 * time.Minute)
	r.put(s1, "a", "session.idle", false, "session.idle_since", idleSince)
	r.units.SetDue(idleSince.Add(2 * time.Hour))
	r.sweep()
	g := r.log.Gen()
	if due := r.e.Due(); !due.Equal(t0.Add(30 * time.Minute)) {
		t.Errorf("Due = %v, want idle_since + cleanup.idle", due)
	}
	if r.log.Gen() != g {
		t.Error("Due noted the log")
	}
}

func TestSeenIsTheSnapshotGeneration(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.units.SetSeen(7)
	if r.e.Seen() != 0 {
		t.Fatal("Seen before a sweep")
	}
	r.sweep()
	if r.e.Seen() != 7 {
		t.Errorf("Seen = %d, want 7", r.e.Seen())
	}
}

func TestAnEventSeenWhileDownIsHandledOnce(t *testing.T) {
	r := newRig(t, eventWF("closed", unit.Session, "session", always, "closed"))
	r.put(s1, "a")
	r.sweep()
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0})
	r.restart()
	r.sweep()
	r.sweep()
	r.restart()
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s1}) {
		t.Errorf("ran %v, want once", got)
	}
}

func TestANewWorkflowStartsAtTheHead(t *testing.T) {
	r := newRig(t, eventWF("closed", unit.Session, "session", always, "closed"))
	r.put(s1, "a")
	r.put(s2, "b")
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0.Add(-time.Hour)})
	r.sweep()
	if len(r.steps.Ran()) != 0 {
		t.Fatalf("a new workflow handled the backlog: %+v", r.steps.Ran())
	}
	r.events.Append(workflow.Event{Unit: s2, Kind: "closed", At: t0})
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s2}) {
		t.Errorf("ran %v", got)
	}
}

func TestTheCursorAdvancesOnlyAfterSave(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, eventWF("closed", unit.Session, "session", always, "closed"))
	r.put(s1, "a")
	r.sweep()
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0})
	r.ledger.FailSave(errors.New("disk full"))
	if _, _, err := r.e.Sweep(ctx, r.loop); err == nil {
		t.Fatal("a failed save did not fail the sweep")
	}
	if cur, _, _, _ := r.events.Cursor(ctx, "closed"); cur != 0 {
		t.Fatalf("the cursor advanced to %d past an unsaved plan", cur)
	}
	r.ledger.FailSave(nil)
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("ran %v after the save worked", got)
	}
	if cur, _, _, _ := r.events.Cursor(ctx, "closed"); cur != 1 {
		t.Errorf("cursor = %d, want 1", cur)
	}
}

func TestAnUnfilledMergedEventStillMatchesOnFacts(t *testing.T) {
	when := automationtest.MatchFunc(func(v workflow.Vars) bool {
		at, _ := v.Facts["pr.merged_at"].(time.Time)
		return v.Trigger["trigger.event.kind"] == "merged" && !at.IsZero()
	}, "pr.merged_at", "trigger.event.kind")
	r := newRig(t, eventWF("merged", unit.GitHub, "pr", when, "merged"))
	r.put(p1, "h1", "pr.merged_at", t0.Add(-time.Minute))
	r.put(s1, "a", "pr.merged_at", t0.Add(-time.Minute))
	r.sweep()
	r.events.Append(workflow.Event{Unit: p1, Kind: "merged", At: t0}, workflow.Event{Unit: s1, Kind: "merged", At: t0})
	r.sweep()
	ran := r.steps.Ran()
	if len(ran) != 1 || ran[0].Unit != p1 {
		t.Fatalf("Ran = %+v; want the pull request only", ran)
	}
	why := ran[0].Why
	if why.Event == nil || why.Event.Data != "" || len(why.Matched) != 2 || why.Matched[0].Name != "pr.merged_at" ||
		!why.Matched[0].Value.(time.Time).Equal(t0.Add(-time.Minute)) {
		t.Errorf("Why = %+v", why)
	}
}

func TestAMissedFiringRunsOnceOnWakeTheSameDay(t *testing.T) {
	r := newRig(t, scheduleWF(t, "daily", "0 9 * * *"))
	r.put(s1, "a")
	r.clock.Set(t0.Add(-2 * time.Hour))
	r.sweep()
	r.clock.Set(t0.Add(time.Hour))
	r.sweep()
	r.sweep()
	ran := r.steps.Ran()
	if len(ran) != 1 || !ran[0].Why.FiredAt.Equal(t0.Add(-time.Hour)) {
		t.Fatalf("Ran = %+v; want the 9:00 firing once", ran)
	}
	r.clock.Set(t0.Add(46 * time.Hour))
	r.sweep()
	if len(r.steps.Ran()) != 1 {
		t.Errorf("a firing missed on another day ran: %+v", r.steps.Ran())
	}
}

func TestLastFireSurvivesARestart(t *testing.T) {
	r := newRig(t, scheduleWF(t, "daily", "0 9 * * *"))
	r.put(s1, "a")
	r.clock.Set(t0.Add(-2 * time.Hour))
	r.sweep()
	r.clock.Set(t0)
	r.sweep()
	r.restart()
	r.clock.Advance(time.Hour)
	r.sweep()
	if len(r.steps.Ran()) != 1 {
		t.Errorf("Ran = %+v; want once across the restart", r.steps.Ran())
	}
}

func TestSweepDoesNothingWhileTheManagerIsUnreachable(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.reach = errors.New("orca is not running")
	r.trace = nil
	_, skipped, err := r.e.Sweep(context.Background(), r.loop)
	if err != nil || !strings.Contains(skipped, "orca is not running") {
		t.Errorf("Sweep = %q, %v", skipped, err)
	}
	if len(r.trace) != 0 || r.state.loads != 0 {
		t.Errorf("an unreachable manager still called %v", r.trace)
	}
}

func TestRefreshAndSettleRunOnlyInSweep(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	var refreshed, settled int
	var got automation.State
	r.units.OnRefresh(func(context.Context) error { refreshed++; return nil })
	r.units.OnSettle(func(_ context.Context, st automation.State) error { settled++; got = st; return nil })
	r.evaluate()
	if refreshed != 0 || settled != 0 {
		t.Fatalf("Evaluate refreshed %d, settled %d", refreshed, settled)
	}
	r.sweep()
	if refreshed != 1 || settled != 1 || got == nil || r.state.loads != 1 || r.state.saves != 1 {
		t.Errorf("Sweep refreshed %d, settled %d with %v, loads %d, saves %d", refreshed, settled, got, r.state.loads, r.state.saves)
	}
}

func TestTheWhyCarriesTriggerAndMatchedFacts(t *testing.T) {
	when := automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Facts["session.idle"] == true },
		"session.idle", "config.cleanup.idle", "session.agent_state")
	r := newRig(t, sweepWF("close", when, "session.close"))
	r.put(s1, "a", "session.agent_state", "done")
	it, _ := planItem(r.evaluate(), "close", s1)
	want := []workflow.Fact{{Name: "config.cleanup.idle", Value: 2 * time.Hour}, {Name: "session.agent_state", Value: "done"},
		{Name: "session.idle", Value: true}}
	if it.Why.Workflow != "close" || it.Why.Trigger != workflow.TriggerSweep || !slices.Equal(it.Why.Matched, want) {
		t.Errorf("Why = %+v", it.Why)
	}
}

func TestOneWorkflowFailingDoesNotStopAnother(t *testing.T) {
	broken := sweepWF("broken", always, "workspace.remove")
	broken.Provider, broken.Slot = unit.Workspace, "workspace"
	r := newRig(t, broken, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.put(w1, "w")
	r.units.FailList(unit.Workspace, errors.New("git is gone"))
	p := r.evaluate()
	if len(p.Errors) != 1 || !strings.Contains(p.Errors[0], "broken") {
		t.Errorf("Errors = %v", p.Errors)
	}
	r.sweep()
	if got := r.ran("session.close"); len(got) != 1 {
		t.Errorf("ran %v", got)
	}
}

func TestGainIsAskedOnlyForItems(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.put(s2, "b", "session.idle", false)
	r.units.SetGain(s1, workflow.Gain{MemoryBytes: 300 << 20})
	p := r.evaluate()
	if got := r.units.GainAsked(); !slices.Equal(got, []unit.Unit{s1}) {
		t.Errorf("Gain asked for %v", got)
	}
	if it, _ := planItem(p, "close", s1); it.Gain.MemoryBytes != 300<<20 {
		t.Errorf("item gain = %+v", it.Gain)
	}
}

func TestADisabledWorkflowLeavesThePlanAndNeverRuns(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	r.wfs = nil
	r.clock.Advance(time.Hour)
	if err := r.e.DueRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.sweep()
	if len(r.steps.Ran()) != 0 || len(r.items()) != 0 {
		t.Errorf("a disabled workflow ran %+v, kept %+v", r.steps.Ran(), r.items())
	}
}

func TestApplyByHandChecksEveryStepBeforeRunningAny(t *testing.T) {
	r := newRig(t, sweepWF("label", idle, "session.mark_closed", "label.add"))
	r.put(s1, "a")
	r.sweep()
	r.steps.Refuse("label.add", s1, "not yours to label")
	if _, err := r.e.Act(context.Background(), r.mustItem("label", s1).ID, workflow.ActionApply, ""); !errors.As(err, new(automation.Refused)) {
		t.Fatalf("Act = %v", err)
	}
	if len(r.steps.Ran()) != 0 {
		t.Errorf("a step ran before a later step's guard refused: %+v", r.steps.Ran())
	}
}

func TestARunTimeRefusalKeepsAttempts(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	noteOnMoved(t, r, func() { r.steps.Refuse("session.close", s1, "the user typed") })
	r.put(s1, "a")
	r.sweep()
	it := r.mustItem("close", s1)
	run, ok, _ := r.ledger.Run(context.Background(), it.Key)
	if !ok || run.Status != workflow.RunRefused || run.Attempts != 0 || it.Attempts != 0 || it.Decision != workflow.DecisionBlocked {
		t.Errorf("run %+v, item %+v; a refusal is not an attempt", run, it)
	}
}

func TestAnEventWhoseFactsFailIsHandledLater(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, eventWF("closed", unit.Session, "session", always, "closed"))
	r.put(s1, "a")
	r.put(s2, "b")
	r.sweep()
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0}, workflow.Event{Unit: s2, Kind: "closed", At: t0})
	r.units.FailFacts(s1, errors.New("database is locked"))
	r.sweep()
	if cur, _, _, _ := r.events.Cursor(ctx, "closed"); cur != 0 {
		t.Fatalf("the cursor moved to %d past an event that was not decided", cur)
	}
	r.units.FailFacts(s1, nil)
	r.sweep()
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s2, s1}) {
		t.Errorf("ran %v; want each event handled once", got)
	}
	if cur, _, _, _ := r.events.Cursor(ctx, "closed"); cur != 2 {
		t.Errorf("cursor = %d, want 2", cur)
	}
}

// pauseLock sets the pause as the sweep takes the loop lock.
type pauseLock struct {
	r *rig
	sync.Mutex
}

func (l *pauseLock) Lock() {
	l.Mutex.Lock()
	l.r.pause = automation.Pause{On: true}
}

func TestAPauseSetBeforeTheLockDropsTheRun(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	sum, _, err := r.e.Sweep(context.Background(), &pauseLock{r: r})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.steps.Ran()) != 0 || !strings.Contains(sum, "1 dropped") {
		t.Errorf("a pause set before the lock still ran %+v (%s)", r.steps.Ran(), sum)
	}
}

func TestDueRunHoldsUnderPauseAndOutsideApply(t *testing.T) {
	ctx := context.Background()
	w := windowed(sweepWF("close", idle, "session.close"), 5*time.Minute)
	r := newRig(t, w)
	r.put(s1, "a")
	r.sweep()
	r.clock.Advance(time.Hour)
	r.pause = automation.Pause{On: true}
	if err := r.e.DueRun(ctx); err != nil {
		t.Fatal(err)
	}
	r.pause = automation.Pause{}
	w.Mode = workflow.ModeObserve
	r.wfs = []workflow.Workflow{w}
	if err := r.e.DueRun(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.steps.Ran()) != 0 {
		t.Errorf("DueRun ran %+v under the pause or in observe", r.steps.Ran())
	}
}

func TestItemsWaitingForTheUserTakeNoSlot(t *testing.T) {
	wait := sweepWF("wait", idle, "session.close")
	wait.Approval.Default = workflow.OutcomeWait
	wait.Limits = workflow.Limits{PerRun: 1, PerDay: 1}
	observe := sweepWF("observe", idle, "session.close")
	observe.Mode = workflow.ModeObserve
	observe.Limits.PerRun = 1
	runs := sweepWF("runs", idle, "session.close")
	runs.Order = -1
	runs.Limits.PerRun = 1
	r := newRig(t, wait, observe, windowed(runs, 5*time.Minute))
	r.put(s1, "a")
	r.put(s2, "b")
	p := r.evaluate()
	for _, wf := range []string{"wait", "observe"} {
		for _, u := range []unit.Unit{s1, s2} {
			if it, _ := planItem(p, wf, u); it.Decision != workflow.DecisionPending {
				t.Errorf("%s on %s = %+v; a waiting item takes no slot", wf, u, it)
			}
		}
	}
	if it, _ := planItem(p, "runs", s2); it.Reason != "limit per_run 1 reached" {
		t.Errorf("a pending default-run item took no slot: %+v", it)
	}
}

func TestAFailedActStillSavesTheState(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	saves := r.state.saves
	r.steps.Fail("session.close", s1, errors.New("orca said no"))
	if _, err := r.e.Act(context.Background(), r.mustItem("close", s1).ID, workflow.ActionApply, ""); err == nil {
		t.Fatal("a failed step did not fail Act")
	}
	if r.state.saves != saves+1 {
		t.Errorf("saves = %d, want %d: the state a step wrote must be kept", r.state.saves, saves+1)
	}
}

func TestASweepWhoseEveryStepFailedFails(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.put(s2, "b")
	r.steps.Fail("session.close", s1, errors.New("boom"))
	r.steps.Fail("session.close", s2, errors.New("boom"))
	if _, _, err := r.e.Sweep(context.Background(), r.loop); err == nil || r.state.saves != 1 {
		t.Errorf("Sweep = %v with %d state saves; want an error and the state saved", err, r.state.saves)
	}
	r.steps.Fail("session.close", s2, nil)
	r.clock.Advance(time.Hour)
	if _, _, err := r.e.Sweep(context.Background(), r.loop); err != nil {
		t.Errorf("a sweep where one step ran failed: %v", err)
	}
}

func TestAGoneUnitsEventIsPassedAtOnce(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, eventWF("closed", unit.Session, "session", always, "closed"))
	r.put(s1, "a")
	r.put(s2, "b")
	r.sweep()
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0}, workflow.Event{Unit: s2, Kind: "closed", At: t0})
	r.units.Remove(s1)
	r.sweep()
	if cur, _, _, _ := r.events.Cursor(ctx, "closed"); cur != 2 {
		t.Errorf("cursor = %d; a gone unit's event is passed at once", cur)
	}
	r.put(s2, "b2")
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s2}) {
		t.Errorf("ran %v; want the other unit's event once", got)
	}
}

func TestAnUndecidableEventHoldsForAnHourWithoutReplayingLaterOnes(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, eventWF("closed", unit.Session, "session", always, "closed"))
	r.put(s1, "a")
	r.put(s2, "b")
	r.sweep()
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0}, workflow.Event{Unit: s2, Kind: "closed", At: t0})
	r.units.FailFacts(s1, errors.New("database is locked"))
	r.sweep()
	r.put(s2, "b2")
	r.clock.Advance(59 * time.Minute)
	r.sweep()
	if cur, _, _, _ := r.events.Cursor(ctx, "closed"); cur != 0 {
		t.Fatalf("cursor = %d; the failing event holds it for an hour", cur)
	}
	r.clock.Advance(time.Minute)
	r.sweep()
	if cur, _, _, _ := r.events.Cursor(ctx, "closed"); cur != 2 {
		t.Errorf("cursor = %d; the event is passed after the hour", cur)
	}
	if !slices.ContainsFunc(r.ledger.Events(), func(ev port.Event) bool {
		return ev.Kind == workflow.KindStepFailed && ev.Unit == s1 && ev.Data == "database is locked"
	}) {
		t.Errorf("no workflow_failed event for the passed event: %+v", r.ledger.Events())
	}
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s2}) {
		t.Errorf("ran %v; want the other unit's event once", got)
	}
}

func TestADueRunDropsAnItemWhoseSourceMoved(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	r.units.SetMoved(s1)
	r.clock.Advance(6 * time.Minute)
	if err := r.e.DueRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.steps.Ran()) != 0 {
		t.Fatalf("a moved source ran: %v", r.steps.Ran())
	}
	if it, ok := r.item("close", s1); ok {
		t.Fatalf("the moved item stayed: %+v", it)
	}
	if run, ok, _ := r.ledger.Run(context.Background(), workflow.NewKey(r.wfs[0], s1, workflow.Facts{"session.fingerprint": "a"}, time.Time{})); ok {
		t.Fatalf("the drop marked the key: %+v", run)
	}
	r.units.SetMoved()
	r.sweep()
	if it := r.mustItem("close", s1); it.Decision != workflow.DecisionPending {
		t.Fatalf("not planned again: %+v", it)
	}
}

func TestAnApplyOfAMovedSourceIsRefused(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	r.units.SetMoved(s1)
	got, err := r.e.Act(context.Background(), r.mustItem("close", s1).ID, workflow.ActionApply, "")
	var refused automation.Refused
	if !errors.As(err, &refused) || got.Decision != workflow.DecisionBlocked || len(r.steps.Ran()) != 0 {
		t.Fatalf("apply: %+v %v, ran %v", got, err, r.steps.Ran())
	}
}

func TestADueRunDropsAnItemWhoseKeyFactsMoved(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	r.put(s1, "b")
	r.clock.Advance(6 * time.Minute)
	if err := r.e.DueRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.steps.Ran()) != 0 {
		t.Fatalf("an item planned on other facts ran: %v", r.steps.Ran())
	}
	r.sweep()
	if it := r.mustItem("close", s1); it.Decision != workflow.DecisionPending || !it.Deadline.Equal(r.clock.Now().Add(5*time.Minute)) {
		t.Fatalf("not planned again with a window of its own: %+v", it)
	}
}

func TestAnApplyOfAnItemWhoseKeyFactsMovedIsRefused(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	r.put(s1, "b")
	got, err := r.e.Act(context.Background(), r.mustItem("close", s1).ID, workflow.ActionApply, "")
	var refused automation.Refused
	if !errors.As(err, &refused) || got.Decision != workflow.DecisionBlocked || len(r.steps.Ran()) != 0 {
		t.Fatalf("apply: %+v %v, ran %v", got, err, r.steps.Ran())
	}
}

// actLock acts on the plan as the sweep takes the loop lock, as an Act
// waiting on the lock would between the plan's save and the run.
type actLock struct {
	sync.Mutex
	act func()
}

func (l *actLock) Lock() {
	l.Mutex.Lock()
	if l.act != nil {
		l.act()
		l.act = nil
	}
}

func TestAnItemActedOnBeforeTheLockIsNotRunAgain(t *testing.T) {
	for _, a := range []workflow.Action{workflow.ActionDismiss, workflow.ActionSkip, workflow.ActionApply} {
		t.Run(string(a), func(t *testing.T) {
			ctx := context.Background()
			r := newRig(t, sweepWF("close", idle, "session.close"))
			r.put(s1, "a")
			var acted workflow.Item
			l := &actLock{act: func() {
				acted = r.mustItem("close", s1)
				if _, err := r.e.Act(ctx, acted.ID, a, ""); err != nil {
					t.Fatal(err)
				}
			}}
			sum, _, err := r.e.Sweep(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if a == workflow.ActionApply {
				want = 1
			}
			if got := r.ran("session.close"); len(got) != want || !strings.Contains(sum, "1 dropped") {
				t.Errorf("ran %v after %s (%s), want %d run(s) and the planned run dropped", got, a, sum, want)
			}
			run, ok, err := r.ledger.Run(ctx, acted.Key)
			if err != nil || !ok {
				t.Fatalf("Run(%v) = %v, %v", acted.Key, ok, err)
			}
			if wantStatus := map[bool]workflow.RunStatus{true: workflow.RunDone, false: workflow.RunDismissed}[want == 1]; run.Status != wantStatus {
				t.Errorf("the key's run is %s, want %s", run.Status, wantStatus)
			}
		})
	}
}

func TestAGuidedRunThatDoesNotFinishKeepsTheGuidance(t *testing.T) {
	for name, fail := range map[string]error{
		"refused":   automation.Refused{Reason: "you are active in it"},
		"transient": automation.Transient{Err: errors.New("orca unreachable")},
		"failed":    errors.New("boom"),
		"done":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			w := windowed(sweepWF("review", always, "turn.start"), 5*time.Minute)
			w.Provider, w.Slot = unit.GitHub, "pr"
			r := newRig(t, w)
			r.put(p1, "h1")
			r.guidance.Set(p1, "check the migration")
			r.steps.Fail("turn.start", p1, fail)
			r.sweepMayFail()
			g, err := r.guidance.Peek(ctx, p1)
			if err != nil {
				t.Fatal(err)
			}
			want := "check the migration"
			if fail == nil {
				want = ""
			}
			if g != want {
				t.Errorf("the guidance after a %s run is %q, want %q", name, g, want)
			}
		})
	}
}

func TestAStoredEventItemSurvivesAFailedFactsRead(t *testing.T) {
	r := newRig(t, windowed(eventWF("closed", unit.Session, "session", always, "closed"), 5*time.Minute))
	r.put(s1, "a")
	r.sweep()
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0})
	r.sweep()
	before := r.mustItem("closed", s1)
	r.units.FailFacts(s1, errors.New("database is locked"))
	r.sweep()
	if got, ok := r.item("closed", s1); !ok || got.ID != before.ID || got.Decision != workflow.DecisionPending {
		t.Fatalf("after a failed facts read the item is %+v (%v), want %+v kept", got, ok, before)
	}
	r.units.FailFacts(s1, nil)
	r.clock.Advance(10 * time.Minute)
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s1}) {
		t.Errorf("ran %v; want the event handled once", got)
	}
}

func TestACarriedRunItemDoesNotRunOnStaleFacts(t *testing.T) {
	r := newRig(t, eventWF("closed", unit.Session, "session", always, "closed"))
	r.put(s1, "a")
	r.sweep()
	r.events.Append(workflow.Event{Unit: s1, Kind: "closed", At: t0})
	r.steps.Fail("session.close", s1, automation.Transient{Err: errors.New("orca unreachable")})
	r.sweep()
	if it := r.mustItem("closed", s1); it.Decision != workflow.DecisionRun {
		t.Fatalf("item = %+v, want a run item left after a transient failure", it)
	}
	r.steps.Fail("session.close", s1, nil)
	r.units.FailFacts(s1, errors.New("database is locked"))
	r.sweep()
	if got := r.ran("session.close"); len(got) != 0 {
		t.Fatalf("ran %v on facts that could not be read", got)
	}
	r.mustItem("closed", s1)
	r.units.FailFacts(s1, nil)
	r.sweep()
	if got := r.ran("session.close"); !slices.Equal(got, []unit.Unit{s1}) {
		t.Errorf("ran %v; want the event handled once", got)
	}
}

func TestAnItemDeferredByHandBeforeTheLockIsNotStartedAgain(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.steps.Defer("session.close", s1, "started")
	l := &actLock{act: func() {
		if _, err := r.e.Act(ctx, r.mustItem("close", s1).ID, workflow.ActionApply, ""); err != nil {
			t.Fatal(err)
		}
	}}
	sum, _, err := r.e.Sweep(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sum, "1 dropped") {
		t.Errorf("the run deferred by hand was started again: %s", sum)
	}
}

// unhandled matches while the unit's head is not the workflow's head mark.
func unhandled(name string) workflow.Predicate {
	return marksReading{automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Marks[name+"/head"] != v.Facts["session.head"] }, "session.head")}
}

func TestARunRecordsItsMarksAndTheNextEvaluationReadsThem(t *testing.T) {
	w := sweepWF("once", unhandled("once"), "session.close")
	w.Marks = map[string]string{"head": "session.head"}
	r := newRig(t, w)
	r.e.Marks = r.ledger
	r.put(s1, "a", "session.head", "h1")
	r.sweep()
	if got := r.ledger.MarksOf(s1); got["once/head"] != "h1" {
		t.Fatalf("marks after the run: %v", got)
	}
	r.put(s1, "b", "session.head", "h1")
	if p := r.evaluate(); len(p.Items) != 0 {
		t.Fatalf("a handled head planned under a new key: %+v", p.Items)
	}
	r.put(s1, "b", "session.head", "h2")
	r.sweep()
	if got := r.ran("session.close"); len(got) != 2 {
		t.Fatalf("ran %v, want a run per head", got)
	}
}

func TestADismissRecordsTheItemsMarks(t *testing.T) {
	w := windowed(sweepWF("once", unhandled("once"), "session.close"), 5*time.Minute)
	w.Key, w.Marks = []string{"session.head"}, map[string]string{"head": "session.head"}
	r := newRig(t, w)
	r.e.Marks = r.ledger
	r.put(s1, "a", "session.head", "h1")
	r.sweep()
	it := r.mustItem("once", s1)
	if it.Marks["once/head"].Value != "h1" {
		t.Fatalf("the item's marks %v", it.Marks)
	}
	if _, err := r.e.Act(context.Background(), it.ID, workflow.ActionDismiss, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.ledger.MarksOf(s1); got["once/head"] != "h1" {
		t.Fatalf("marks after the dismiss: %v", got)
	}
}

type marksReading struct{ workflow.Predicate }

func (marksReading) MarkNames() []string { return []string{"once/head"} }

func TestOnlyAWorkflowThatReadsMarksLoadsThem(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.e.Marks = r.ledger
	r.put(s1, "a")
	r.evaluate()
	if slices.ContainsFunc(r.trace, func(c string) bool { return strings.HasSuffix(c, "ledger.marks") }) {
		t.Fatalf("a workflow without handled() read marks: %v", r.trace)
	}
	r.wfs = []workflow.Workflow{sweepWF("close", marksReading{idle}, "session.close")}
	r.evaluate()
	if !slices.ContainsFunc(r.trace, func(c string) bool { return strings.HasSuffix(c, "ledger.marks") }) {
		t.Fatalf("a workflow with handled() read no marks: %v", r.trace)
	}
}
