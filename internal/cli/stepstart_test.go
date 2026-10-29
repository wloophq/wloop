package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// fakePlanner is a plugin's planner that plans fixed rounds, by unit key,
// and reads each pull request as the github planner read it; it keeps the
// round state each plan read.
type fakePlanner struct {
	mu     sync.Mutex
	rounds map[string][]src.Round
	states []src.RoundState
}

func (f *fakePlanner) Plan(_ context.Context, u plugin.Unit, st src.RoundState) (src.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, st)
	return src.Plan{Rounds: slices.Clone(f.rounds[u.Key])}, nil
}

// round is the round name of key's plan; false when it plans none.
func (f *fakePlanner) round(key, name string) (src.Round, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.rounds[key], func(r src.Round) bool { return r.Name == name })
	if i < 0 {
		return src.Round{}, false
	}
	return f.rounds[key][i], true
}

// edit changes the round name of key's plan.
func (f *fakePlanner) edit(key, name string, fn func(r *src.Round)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rounds[key] {
		if f.rounds[key][i].Name == name {
			fn(&f.rounds[key][i])
		}
	}
}

// plannerOf is the fake planner of r's steps.
func (r *startRig) planner() *fakePlanner { return r.steps.d.Planners[unit.GitHub].(*fakePlanner) }

// plannedRounds are the rounds the github planner plans on sc's pull
// request; it fails without one.
func plannedRounds(t *testing.T, sc sweepCase) *fakePlanner {
	t.Helper()
	out := offeredRounds(t, sc)
	if len(out.rounds) == 0 {
		t.Fatal("the github planner plans no round")
	}
	return out
}

// livePlanner is the GitHub plugin's planner of sc's pull request, once
// it read sc's fake GitHub.
func livePlanner(t *testing.T, sc sweepCase) roundPlanner {
	t.Helper()
	te := sc.te
	if err := te.syncInventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	linkWorkspaces(t, te)
	return te.githubPluginOf()
}

// offeredRounds are the rounds the github planner plans on sc's pull
// request over its stored round state, frozen.
func offeredRounds(t *testing.T, sc sweepCase) *fakePlanner {
	t.Helper()
	ctx := context.Background()
	gh := livePlanner(t, sc)
	u := sc.ref.Unit()
	stored, err := sc.te.Rounds.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sc.env.roundStateOf(ctx, u, stored, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := gh.Plan(ctx, psource.Wire(u), st)
	if err != nil {
		t.Fatal(err)
	}
	return &fakePlanner{rounds: map[string][]src.Round{u.Key: plan.Rounds}}
}

// fakeBriefs answers every turn with rd and counts what asked.
type fakeBriefs struct {
	rd          intake.Readiness
	peeks, gets int
	intakes     []intake.Intake
}

func (f *fakeBriefs) Peek(context.Context, unit.Unit, bool, []string) (intake.Readiness, error) {
	f.peeks++
	return f.rd, nil
}

func (f *fakeBriefs) Ready(context.Context, unit.Unit, bool, []string) (intake.Readiness, error) {
	f.gets++
	return f.rd, nil
}

func (f *fakeBriefs) BriefReady(ctx context.Context, u unit.Unit) (intake.Readiness, error) {
	return f.Ready(ctx, u, true, nil)
}

func (f *fakeBriefs) OpenIntakes(context.Context) ([]intake.Intake, error) { return f.intakes, nil }

// startRig is a stepSet over a scenario the github facts offer one round on.
type startRig struct {
	sc     sweepCase
	rec    *memRecorder
	units  *automationtest.Units
	briefs *fakeBriefs
	steps  *stepSet
	u      unit.Unit
	st     workflow.Step
	why    workflow.Why
}

func newStartRig(t *testing.T, sc sweepCase, intent, round string) *startRig {
	t.Helper()
	rec := &memRecorder{}
	sc.env.Rec = rec
	planner := plannedRounds(t, sc)
	briefs := &fakeBriefs{rd: intake.Readiness{Go: true}}
	sc.env.Briefing = briefs
	u := sc.ref.Unit()
	units := automationtest.NewUnits()
	units.Put(u, workflow.Facts{"pr.repo": "acme/api", "pr.held": false, "pr.stale": false, "pr.state": "OPEN",
		"pr.review.reason": "the author answered", "pr.address.reason": "reviewed by bob", "pr.first.reason": "my review was requested"})
	r := &startRig{sc: sc, rec: rec, units: units, briefs: briefs, u: u,
		st:  workflow.Step{Use: builtin.StepTurnStart, Args: map[string]any{"intent": intent, "stage": round}},
		why: workflow.Why{Workflow: "review-follow-up", Trigger: workflow.TriggerSweep, Matched: []workflow.Fact{{Name: "pr.review.action", Value: "follow-up"}}}}
	r.steps = newStepSet(sc.env, stepDeps{Units: units, Planners: map[unit.ProviderID]roundPlanner{unit.GitHub: planner}, Briefs: briefs, Rounds: sc.te.Rounds})
	return r
}

// load binds the stored round state, as the engine's Load does.
func (r *startRig) load(t *testing.T) *round.Store {
	t.Helper()
	st, err := r.steps.states().Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.(*roundState).st
}

func (r *startRig) save(t *testing.T) {
	t.Helper()
	if st := r.steps.bound(); st != nil {
		if err := (&roundState{st: st, rounds: r.sc.te.Rounds, steps: r.steps}).Save(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *startRig) check(t *testing.T, o automation.CheckOpts) string {
	t.Helper()
	refusal, err := r.steps.Check(context.Background(), r.st, r.u, r.why, o)
	if err != nil {
		t.Fatal(err)
	}
	return refusal
}

func (r *startRig) run(t *testing.T, o automation.RunOpts) (string, error) {
	t.Helper()
	r.load(t)
	defer r.save(t)
	return r.steps.Run(context.Background(), r.st, r.u, r.why, o)
}

func trustBlockEvent(ref pr.Ref, at time.Time) dinv.Event {
	b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/src/api", Folder: "/wt/rev", Round: "follow-up"}
	return dinv.Event{PR: ref, Kind: dinv.KindRoundBlocked, Key: b.Key(at), At: at, Data: "could not start: /wt/rev is not trusted in the agent home of its terminal"}
}

func TestTurnStartGuardsInTheBasesOrder(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	ref := sc.ref
	st := r.load(t)
	sc.te.cfg.Turns.PerUnitPerDay = 1
	st.Count(ref.String(), testNow)
	guards := []struct {
		want     string
		set, off func()
	}{
		{"held by you", func() { r.units.Set(r.u, "pr.held", true) }, func() { r.units.Set(r.u, "pr.held", false) }},
		{whyPaused, func() { sc.te.setPause(t, round.Pause{On: true}) }, func() { sc.te.setPause(t, round.Pause{}) }},
		{"its stored detail is stale", func() { r.units.Set(r.u, "pr.stale", true) }, func() { r.units.Set(r.u, "pr.stale", false) }},
		{errBusy.Error(), func() { sc.ws.Activity = map[string]wsdomain.Activity{"ws-rev": {Busy: true}} }, func() { sc.ws.Activity = nil }},
		{"its review turn asks you something", func() { r.rec.questions = map[pr.Ref]map[string]bool{ref: {"review": true}} }, func() { r.rec.questions = nil }},
		{"an intake of yours asks for it", func() { r.briefs.intakes = []intake.Intake{{Unit: r.u, Intent: dturn.Intent(dintent.Review)}} }, func() { r.briefs.intakes = nil }},
		{"round blocked: could not start", func() { r.rec.events = append(r.rec.events, trustBlockEvent(ref, testNow)) }, func() { r.rec.events = nil }},
		{"brief not ready", func() { r.briefs.rd = intake.Readiness{Wait: intake.WaitBriefing, Attempt: 1} }, func() { r.briefs.rd = intake.Readiness{Go: true} }},
		{"turns.per_unit_per_day reached (1)", func() {}, func() { sc.te.cfg.Turns.PerUnitPerDay = 5 }},
	}
	for _, g := range guards {
		g.set()
	}
	for _, g := range guards {
		if got := r.check(t, automation.CheckOpts{Observe: false, ByHand: true}); !strings.HasPrefix(got, g.want) {
			t.Fatalf("refusal %q, want %q", got, g.want)
		}
		g.off()
	}
	if got := r.check(t, automation.CheckOpts{Observe: false, ByHand: true}); got != "" {
		t.Fatalf("with every guard off: %q", got)
	}
}

func TestAddressRefusesOnlyAWaitingAgent(t *testing.T) {
	t.Parallel()
	sc := addressScenario(t)
	r := newStartRig(t, sc, string(dintent.Address), "")
	planned, _ := r.planner().round(r.u.Key, roundAddress)
	for name, c := range map[string]struct {
		a    wsdomain.Activity
		want string
	}{
		"busy":        {wsdomain.Activity{Busy: true}, ""},
		"user active": {wsdomain.Activity{UserActive: true}, ""},
		"waiting":     {wsdomain.Activity{Waiting: true}, errWaiting.Error()},
	} {
		sc.ws.Activity = map[string]wsdomain.Activity{planned.Workspace.ID: c.a}
		if got := r.check(t, automation.CheckOpts{Observe: false}); got != c.want {
			t.Errorf("%s: refusal %q, want %q", name, got, c.want)
		}
	}
}

func TestReviewRefusesBusyAndUserActive(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	for name, c := range map[string]struct {
		a      wsdomain.Activity
		byHand bool
		want   string
	}{
		"busy":                {wsdomain.Activity{Busy: true}, false, errBusy.Error()},
		"user active":         {wsdomain.Activity{UserActive: true}, false, errUserActive.Error()},
		"waiting":             {wsdomain.Activity{Waiting: true}, false, ""},
		"user active by hand": {wsdomain.Activity{UserActive: true}, true, ""},
		"busy by hand":        {wsdomain.Activity{Busy: true}, true, errBusy.Error()},
	} {
		sc.ws.Activity = map[string]wsdomain.Activity{"ws-rev": c.a}
		if got := r.check(t, automation.CheckOpts{Observe: false, ByHand: c.byHand}); got != c.want {
			t.Errorf("%s: refusal %q, want %q", name, got, c.want)
		}
	}
}

func TestAStaleDetailRefuses(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	r.units.Set(r.u, "pr.stale", true)
	if got := r.check(t, automation.CheckOpts{Observe: false}); got != "its stored detail is stale" {
		t.Fatalf("refusal %q", got)
	}
}

func TestCheckNeverStartsABrief(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	r.briefs.rd = intake.Readiness{Wait: intake.WaitBriefing, Attempt: 1}
	if got := r.check(t, automation.CheckOpts{Observe: false}); !strings.HasPrefix(got, "brief not ready") {
		t.Fatalf("refusal %q", got)
	}
	if r.briefs.peeks != 1 || r.briefs.gets != 0 {
		t.Fatalf("peeks %d, readies %d", r.briefs.peeks, r.briefs.gets)
	}
	if len(r.rec.events) != 0 {
		t.Fatalf("check recorded %v", r.rec.kinds())
	}
}

func TestNoteStartsTheBriefAndRecordsBriefWaiting(t *testing.T) {
	t.Parallel()
	r := newStartRig(t, followupScenario(t), string(dintent.Review), roundFollowup)
	r.briefs.rd = intake.Readiness{Wait: intake.WaitBriefing, Attempt: 1}
	refusal := r.check(t, automation.CheckOpts{Observe: false})
	r.load(t)
	if err := r.steps.Note(context.Background(), r.st, r.u, r.why, refusal); err != nil {
		t.Fatal(err)
	}
	r.save(t)
	if r.briefs.gets != 1 {
		t.Fatalf("readies %d", r.briefs.gets)
	}
	if !slices.Contains(r.rec.kinds(), kindBriefWaiting) {
		t.Fatalf("events %v", r.rec.kinds())
	}
}

func TestTheCapHoldsOnTheFirstHit(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	sc.te.cfg.Turns.PerUnitPerDay = 1
	st := r.load(t)
	st.Count(sc.ref.String(), testNow)
	r.save(t)
	refusal := r.check(t, automation.CheckOpts{Observe: false})
	if refusal != "turns.per_unit_per_day reached (1)" {
		t.Fatalf("refusal %q", refusal)
	}
	for range 2 {
		r.load(t)
		if err := r.steps.Note(context.Background(), r.st, r.u, r.why, refusal); err != nil {
			t.Fatal(err)
		}
		r.save(t)
	}
	held := 0
	for _, k := range r.rec.kinds() {
		if k == dinv.KindRoundHeld {
			held++
		}
	}
	unread := slices.DeleteFunc(slices.Clone(sc.ws.Writes), func(w string) bool { return w != "unread ws-rev" })
	if held != 1 || len(unread) != 1 {
		t.Fatalf("round_held %d, unread %v", held, unread)
	}
}

func TestDismissWritesSweepSkipsRecords(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		sc            func(*testing.T) sweepCase
		intent, round string
		check         func(*testing.T, *round.Store, src.Round, string)
		kind          string
	}{
		"follow-up": {followupScenario, string(dintent.Review), roundFollowup, func(t *testing.T, st *round.Store, c src.Round, key string) {
			if want := c.OnSkipped[ghsource.MarkReviewed]; st.Mark(key, ghsource.MarkReviewed) != want || want == "" {
				t.Errorf("fingerprint %q, want %q", st.Mark(key, ghsource.MarkReviewed), want)
			}
		}, "follow-up"},
		"triage": {addressScenario, string(dintent.Address), "", func(t *testing.T, st *round.Store, c src.Round, key string) {
			if st.Mark(key, ghsource.MarkAddressedReviews) != c.OnSkipped[ghsource.MarkAddressedReviews] || st.Mark(key, ghsource.MarkAddressedChecks) != c.OnSkipped[ghsource.MarkAddressedChecks] {
				t.Errorf("fingerprint %q, checks head %q; want %+v", st.Mark(key, ghsource.MarkAddressedReviews), st.Mark(key, ghsource.MarkAddressedChecks), c)
			}
		}, "triage"},
	} {
		t.Run(name, func(t *testing.T) {
			sc := c.sc(t)
			r := newStartRig(t, sc, c.intent, c.round)
			planned, _ := r.planner().round(r.u.Key, sc.env.startOf(r.st, r.u.Provider).candidate())
			r.load(t)
			if err := r.steps.Dismiss(context.Background(), r.st, r.u, r.why); err != nil {
				t.Fatal(err)
			}
			r.save(t)
			c.check(t, sc.te.loadRounds(t), planned, sc.ref.String())
			if !slices.ContainsFunc(r.rec.events, func(e dinv.Event) bool {
				return e.Kind == dinv.KindRoundSkipped && e.Data == c.kind+": skipped by you"
			}) {
				t.Errorf("events %+v", r.rec.events)
			}
		})
	}
}

func TestASkippedFollowupDoesNotComeBack(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	r.load(t)
	if err := r.steps.Dismiss(context.Background(), r.st, r.u, r.why); err != nil {
		t.Fatal(err)
	}
	r.save(t)
	if got := offeredRounds(t, sc).rounds[sc.ref.String()]; len(got) != 0 {
		t.Fatalf("the skipped follow-up came back: %+v", got)
	}
}

func TestRunTakesTheStoredGuidance(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	if err := r.rec.KeepNextGuidance(context.Background(), sc.ref, "check the migration"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(sc.ws.Prompts) != 1 || !strings.Contains(turnText(t, sc.te, sc.ws.Prompts[0].Text), "check the migration") {
		t.Fatalf("prompts %v", sc.ws.Prompts)
	}
	if got := r.rec.overrides[sc.ref].NextGuidance; got != "" {
		t.Fatalf("the guidance stayed stored: %q", got)
	}
}

func TestRunGuidanceKeepsTheStoredOne(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	if err := r.rec.KeepNextGuidance(context.Background(), sc.ref, "check the migration"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(t, automation.RunOpts{Guidance: "mind the tests", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if text := turnText(t, sc.te, sc.ws.Prompts[0].Text); !strings.Contains(text, "mind the tests") || strings.Contains(text, "check the migration") {
		t.Fatalf("turn %q", text)
	}
	if got := r.rec.overrides[sc.ref].NextGuidance; got != "check the migration" {
		t.Fatalf("the stored guidance was taken: %q", got)
	}
}

func TestRunReadsCurrentFacts(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	r.why.Matched = []workflow.Fact{{Name: "pr.held", Value: false}}
	r.units.Set(r.u, "pr.held", true)
	_, err := r.run(t, automation.RunOpts{})
	var refused automation.Refused
	if !asRefused(err, &refused) || refused.Reason != "held by you" {
		t.Fatalf("run: %v", err)
	}
	if len(sc.ws.Prompts) != 0 {
		t.Fatalf("sent %v", sc.ws.Prompts)
	}
}

func TestAWorkflowStartedTurnCarriesItsWhy(t *testing.T) {
	t.Parallel()
	t.Run("a round", func(t *testing.T) {
		sc := followupScenario(t)
		r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
		if _, err := r.run(t, automation.RunOpts{}); err != nil {
			t.Fatal(err)
		}
		text := turnText(t, sc.te, sc.ws.Prompts[0].Text)
		if !strings.Contains(text, "from the workflow `review-follow-up`") || !strings.Contains(text, "`pr.review.action`: `follow-up`") {
			t.Fatalf("turn %q", text)
		}
	})
	t.Run("a launch", func(t *testing.T) {
		sc := followupScenario(t)
		sc.te.gh.basics = openBasics
		why := &workflow.Why{Workflow: "review-again", Trigger: workflow.TriggerSweep}
		if err := (cliLauncher{sc.env}).Launch(context.Background(), turns.Launch{Unit: sc.ref.Unit(), Intent: dturn.Intent(dintent.Review), Trigger: dturn.Automated, Why: why}); err != nil {
			t.Fatal(err)
		}
		if len(sc.ws.Prompts) != 1 || !strings.Contains(turnText(t, sc.te, sc.ws.Prompts[0].Text), "from the workflow `review-again`") {
			t.Fatalf("prompts %v", sc.ws.Prompts)
		}
	})
}

func TestATriggerStaysAutomatedWhenAppliedByHand(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	if _, err := r.run(t, automation.RunOpts{ByHand: true, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if len(sc.ws.Prompts) != 1 {
		t.Fatalf("prompts %v", sc.ws.Prompts)
	}
	if h, _, _, _ := promptOf(t, sc.ws.Prompts[0].Text); !strings.HasSuffix(h, "started by "+brand.Name+".") {
		t.Fatalf("headline %q", h)
	}
	books, err := sc.te.Turns.Store.Books(context.Background(), r.u)
	if err != nil {
		t.Fatal(err)
	}
	b := books[r.u]
	if len(b.Turns) == 0 || b.Turns[len(b.Turns)-1].Trigger != dturn.Automated {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestAStartBlockIsABlockedItemNeverTrusted(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	r.rec.events = append(r.rec.events, trustBlockEvent(sc.ref, testNow.Add(-time.Hour)))
	r.units.Put(r.u, workflow.Facts{"pr.repo": "acme/api", "pr.held": false, "pr.stale": false, "pr.fingerprint": "fp"})
	ledger := automationtest.NewLedger()
	eng := &automation.Engine{
		Workflows: func() []workflow.Workflow {
			return []workflow.Workflow{{Name: "review-follow-up", Provider: unit.GitHub, Slot: "pr", Observe: false,
				Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{r.st}, Approval: workflow.Approval{Mode: workflow.ApprovalRun}}}
		},
		Units: r.units, Steps: r.steps, Ledger: ledger, Clock: clockFunc(sc.env.Now), State: r.steps.states(),
	}
	plan, err := eng.Evaluate(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Decision != workflow.DecisionBlocked || !strings.HasPrefix(plan.Items[0].Reason, "round blocked: ") {
		t.Fatalf("plan %+v", plan.Items)
	}
	if err := ledger.Save(context.Background(), automation.PlanWrite{Workflows: []string{"review-follow-up"}, Items: plan.Items}); err != nil {
		t.Fatal(err)
	}
	it, err := eng.Act(context.Background(), plan.Items[0].ID, workflow.ActionApply, "")
	var refused automation.Refused
	if !asRefused(err, &refused) || it.Decision != workflow.DecisionBlocked {
		t.Fatalf("apply: %+v %v", it, err)
	}
	if len(sc.ws.Prompts) != 0 || len(sc.ws.Writes) != 0 {
		t.Fatalf("applied a blocked round: %v %v", sc.ws.Prompts, sc.ws.Writes)
	}
	if slices.Contains(r.rec.kinds(), dinv.KindRoundUnblocked) {
		t.Fatalf("a step cleared the block: %v", r.rec.kinds())
	}
}

func TestAnObservedRoundKeepsTheBasesCaps(t *testing.T) {
	t.Parallel()
	sc := addressScenario(t)
	r := newStartRig(t, sc, string(dintent.Address), "")
	r.observed(roundAddress)
	sc.te.cfg.Turns.PerUnitPerDay = 1
	st := r.load(t)
	st.Count(sc.ref.String(), testNow)
	r.save(t)
	refusal := r.check(t, automation.CheckOpts{Observe: false})
	if refusal != "turns.per_unit_per_day reached (1)" {
		t.Fatalf("observed address: %q", refusal)
	}
	r.load(t)
	if err := r.steps.Note(context.Background(), r.st, r.u, r.why, refusal); err != nil {
		t.Fatal(err)
	}
	r.save(t)
	if slices.Contains(r.rec.kinds(), dinv.KindRoundHeld) || len(sc.ws.Writes) != 0 || len(sc.te.loadRounds(t).HeldToday(testNow)) != 0 {
		t.Fatalf("an observed round was held: %v %v", r.rec.kinds(), sc.ws.Writes)
	}

	fsc := followupScenario(t)
	fr := newStartRig(t, fsc, string(dintent.Review), roundFollowup)
	fr.planner().edit(fsc.ref.String(), roundFollowup, func(r *src.Round) { r.Observed = true })
	fsc.te.cfg.Turns.PerUnitPerDay = 1
	fst := fr.load(t)
	fst.Count(fsc.ref.String(), testNow)
	fr.save(t)
	if got := fr.check(t, automation.CheckOpts{Observe: false}); got != "" {
		t.Fatalf("observed follow-up: %q", got)
	}
}

func TestAnAddressRoundTellsTheLiveSessionAboutLocalWork(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		setup func(te *testEnv)
		local string
	}{
		"uncommitted changes": {func(te *testEnv) { te.git.dirty["/wt/auth"] = true }, "uncommitted changes"},
		"unpushed commits":    {func(te *testEnv) { te.git.ancestor["/wt/auth"] = false }, "commits not on the PR"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sc := addressScenario(t)
			r := newStartRig(t, sc, string(dintent.Address), roundAddress)
			c.setup(sc.te)
			if _, err := r.run(t, automation.RunOpts{}); err != nil {
				t.Fatal(err)
			}
			m := sc.ws
			if len(m.Prompts) != 1 || m.Prompts[0].Handle != "term-auth" {
				t.Fatalf("prompts %+v", m.Prompts)
			}
			if text := turnText(t, sc.te, m.Prompts[0].Text); !strings.Contains(text, "The checkout holds work not on the pull request: "+c.local+".") {
				t.Fatalf("turn:\n%s", text)
			}
			if w := strings.Join(m.Writes, "\n"); strings.Contains(w, "session ws-auth") {
				t.Fatalf("started a fresh session beside the one that knows the local work:\n%s", w)
			}
		})
	}
}

func TestTurnRecordsItsWorkflow(t *testing.T) {
	t.Parallel()
	newestWorkflow := func(t *testing.T, sc sweepCase, u unit.Unit) string {
		t.Helper()
		books, err := sc.te.Turns.Store.Books(context.Background(), u)
		if err != nil {
			t.Fatal(err)
		}
		b := books[u]
		if len(b.Turns) == 0 {
			t.Fatal("no turn")
		}
		return b.Turns[len(b.Turns)-1].Workflow
	}
	t.Run("a round", func(t *testing.T) {
		sc := followupScenario(t)
		r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
		if _, err := r.run(t, automation.RunOpts{}); err != nil {
			t.Fatal(err)
		}
		if got := newestWorkflow(t, sc, r.u); got != "review-follow-up" {
			t.Fatalf("workflow %q", got)
		}
	})
	t.Run("a launch", func(t *testing.T) {
		sc := followupScenario(t)
		sc.te.gh.basics = openBasics
		why := &workflow.Why{Workflow: "review-again", Trigger: workflow.TriggerSweep}
		if err := (cliLauncher{sc.env}).Launch(context.Background(), turns.Launch{Unit: sc.ref.Unit(), Intent: dturn.Intent(dintent.Review), Trigger: dturn.Automated, Why: why}); err != nil {
			t.Fatal(err)
		}
		if got := newestWorkflow(t, sc, sc.ref.Unit()); got != "review-again" {
			t.Fatalf("workflow %q", got)
		}
	})
	t.Run("by hand", func(t *testing.T) {
		sc := followupScenario(t)
		sc.te.gh.basics = openBasics
		if err := (cliLauncher{sc.env}).Launch(context.Background(), turns.Launch{Unit: sc.ref.Unit(), Intent: dturn.Intent(dintent.Review), Trigger: dturn.ByHand}); err != nil {
			t.Fatal(err)
		}
		if got := newestWorkflow(t, sc, sc.ref.Unit()); got != "" {
			t.Fatalf("workflow %q", got)
		}
	})
}

func TestAnAddressRoundSaysWhyNow(t *testing.T) {
	t.Parallel()
	sc := addressScenario(t)
	r := newStartRig(t, sc, string(dintent.Address), roundAddress)
	r.planner().edit(r.u.Key, roundAddress, func(r *src.Round) {
		r.TurnFacts = []src.Fact{{Name: "pr.address.reviewers", Value: src.List("bob", "jussara-ai")}, {Name: "pr.address.checks", Value: src.List()}}
	})
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(sc.ws.Prompts) != 1 {
		t.Fatalf("prompts %+v", sc.ws.Prompts)
	}
	text := turnText(t, sc.te, sc.ws.Prompts[0].Text)
	if !strings.Contains(text, "## Why now") || !strings.Contains(text, "- @bob\n- @jussara-ai\n") {
		t.Fatalf("turn:\n%s", text)
	}
}

// TestAnyTurnOnAPullRequestSpendsTheDailyCap: the budget is the unit's
// plugin's, so an intent holding no role spends it too.
func TestAnyTurnOnAPullRequestSpendsTheDailyCap(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	files := myReview(t)
	m := files["intents/my-review/intent.toml"]
	if !strings.Contains(m, "role    = \"pr.review\"\n") {
		t.Fatal("the bundled review holds no role")
	}
	files["intents/my-review/intent.toml"] = strings.Replace(m, "role    = \"pr.review\"\n", "", 1)
	userProfile(t, sc.te, files)
	r := newStartRig(t, sc, "my-review", roundFollowup)
	sc.te.cfg.Turns.PerUnitPerDay = 1
	st := r.load(t)
	st.Count(sc.ref.String(), testNow)
	r.save(t)
	if refusal := r.check(t, automation.CheckOpts{Observe: false}); refusal != "turns.per_unit_per_day reached (1)" {
		t.Fatalf("refusal %q; broken %v", refusal, sc.env.loadedIntents().Broken)
	}
}

// heldGates are the gates a held round opens, by unit.
type heldGates struct{ open []gate.Gate }

func (g *heldGates) Open(_ context.Context, gt gate.Gate, p gates.Policy) (gate.Gate, error) {
	d, err := p.Decide(context.Background(), gt)
	if err != nil || d.Mode != gate.ModeWait {
		return gate.Gate{}, fmt.Errorf("a held round's gate decided %+v %v; only a person runs it", d, err)
	}
	gt.ID, gt.Status = int64(len(g.open)+1), gate.Open
	g.open = append(g.open, gt)
	return gt, nil
}

func (g *heldGates) Where(_ context.Context, q gates.Query) ([]gate.Gate, error) {
	return slices.DeleteFunc(slices.Clone(g.open), func(gt gate.Gate) bool { return gt.Kind != q.Kind || gt.Unit != q.Unit }), nil
}

func TestAHeldRoundIsAPlanGate(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	hg := &heldGates{}
	r.steps.d.Gates = hg
	sc.te.cfg.Turns.PerUnitPerDay = 1
	st := r.load(t)
	st.Count(sc.ref.String(), testNow)
	r.save(t)
	refusal := r.check(t, automation.CheckOpts{})
	for range 2 {
		r.load(t)
		if err := r.steps.Note(context.Background(), r.st, r.u, r.why, refusal); err != nil {
			t.Fatal(err)
		}
		r.save(t)
	}
	if len(hg.open) != 1 || hg.open[0].Kind != gate.KindPlan || hg.open[0].Unit != r.u || hg.open[0].Subject != round.HeldSubject(testNow) {
		t.Fatalf("the held round's gates %+v, want one plan gate on %s for the day", hg.open, r.u)
	}
}
