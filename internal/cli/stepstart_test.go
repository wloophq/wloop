package cli

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/github/githubtest"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace"
)

// fakeCandidates are the rounds the github facts offer, by unit key and round.
type fakeCandidates map[string]roundCandidate

func (f fakeCandidates) Candidate(_ context.Context, u unit.Unit, round string) (roundCandidate, bool, error) {
	c, ok := f[u.Key+" "+round]
	return c, ok, nil
}

// plannedCandidates are the rounds the github facts offer on sc's pull
// request; it fails without one.
func plannedCandidates(t *testing.T, sc sweepCase) fakeCandidates {
	t.Helper()
	out := offeredRounds(t, sc)
	if len(out) == 0 {
		t.Fatal("the github facts offer no round")
	}
	return out
}

// offeredRounds are the rounds the github facts offer on sc's pull request,
// once the collector read sc's fake GitHub.
func offeredRounds(t *testing.T, sc sweepCase) fakeCandidates {
	t.Helper()
	te := sc.te
	if err := te.syncInventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	linkWorkspaces(t, te)
	cfg := te.cfg
	ws := te.WS
	if !ws.Snapshots() {
		ws = workspace.NewSet(workspace.NewCached(sc.ws, sc.ws, time.Minute))
	}
	mod, err := ghsource.Module(cfg, source.Deps{DB: te.db, Inventory: te.Store, GitHub: te.GitHub, WS: ws, Sink: te.boardSink(cfg),
		Turns: te.Turns, Clock: clockFunc(sc.env.Now), Log: t.Logf, Git: te.Git, Agents: te.Agents,
		Intents: func() *dintent.Registry { return te.loadedIntents().Registry }})
	if err != nil {
		t.Fatal(err)
	}
	cands := candidatesOf([]source.Module{mod})
	out := fakeCandidates{}
	for _, r := range []string{roundFollowup, roundFirst, roundAddress} {
		c, ok, err := cands.Candidate(context.Background(), sc.ref.Unit(), r)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			out[sc.ref.String()+" "+r] = c
		}
	}
	return out
}

// linkWorkspaces runs the collector's fast pass over te's workspaces, which
// links each to its pull request in the inventory as the daemon's
// ingestion does.
func linkWorkspaces(t *testing.T, te *testEnv) {
	t.Helper()
	te.Store.UseClock(te.Now)
	hosted := te.gh.hosted
	te.gh.hosted = true
	defer func() { te.gh.hosted = hosted }()
	c := &ghsource.Collector{Store: te.Store, GH: githubtest.Runner(quietGH{te.gh}), WS: te.WS, Sink: te.boardSink(te.cfg), Cfg: te.cfg, Now: te.Now, Log: t.Logf}
	if _, err := c.Fast(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	cands := plannedCandidates(t, sc)
	briefs := &fakeBriefs{rd: intake.Readiness{Go: true}}
	sc.env.Briefing = briefs
	u := sc.ref.Unit()
	units := automationtest.NewUnits()
	units.Put(u, workflow.Facts{"pr.repo": "acme/api", "pr.held": false, "pr.stale": false, "pr.state": "OPEN",
		"pr.review.reason": "the author answered", "pr.address.reason": "reviewed by bob", "pr.first.reason": "my review was requested"})
	r := &startRig{sc: sc, rec: rec, units: units, briefs: briefs, u: u,
		st:  workflow.Step{Use: builtin.StepTurnStart, Args: map[string]any{"intent": intent, "round": round}},
		why: workflow.Why{Workflow: "review-follow-up", Trigger: workflow.TriggerSweep, Matched: []workflow.Fact{{Name: "pr.review.action", Value: "follow-up"}}}}
	r.steps = newStepSet(sc.env, stepDeps{Units: units, Candidates: cands, Briefs: briefs, Rounds: sc.te.Rounds})
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

func trustBlockEvent(ref github.Ref, at time.Time) dinv.Event {
	b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/src/api", Folder: "/wt/rev", Round: "follow-up"}
	return dinv.Event{PR: ref, Kind: dinv.KindRoundBlocked, Key: b.Key(at), At: at, Data: "could not start: /wt/rev is not trusted in the agent home of its terminal"}
}

func TestTurnStartGuardsInTheBasesOrder(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	ref := sc.ref
	st := r.load(t)
	sc.te.cfg.Rounds.PerPRPerDay = 1
	st.RecordFollowup(ref.String(), round.Reviewed, "", testNow)
	guards := []struct {
		want     string
		set, off func()
	}{
		{"held by you", func() { r.units.Set(r.u, "pr.held", true) }, func() { r.units.Set(r.u, "pr.held", false) }},
		{whyPaused, func() { sc.te.setPause(t, round.Pause{On: true}) }, func() { sc.te.setPause(t, round.Pause{}) }},
		{"its stored detail is stale", func() { r.units.Set(r.u, "pr.stale", true) }, func() { r.units.Set(r.u, "pr.stale", false) }},
		{errBusy.Error(), func() { sc.ws.Activity = map[string]wsdomain.Activity{"ws-rev": {Busy: true}} }, func() { sc.ws.Activity = nil }},
		{"its review turn asks you something", func() { r.rec.questions = map[github.Ref]map[string]bool{ref: {"review": true}} }, func() { r.rec.questions = nil }},
		{"an intake of yours asks for it", func() { r.briefs.intakes = []intake.Intake{{Unit: r.u, Intent: dturn.Intent(dintent.Review)}} }, func() { r.briefs.intakes = nil }},
		{"round blocked: could not start", func() { r.rec.events = append(r.rec.events, trustBlockEvent(ref, testNow)) }, func() { r.rec.events = nil }},
		{"brief not ready", func() { r.briefs.rd = intake.Readiness{Wait: intake.WaitBriefing, Attempt: 1} }, func() { r.briefs.rd = intake.Readiness{Go: true} }},
		{"per-PR daily cap reached (1)", func() {}, func() { sc.te.cfg.Rounds.PerPRPerDay = 5 }},
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
	cand, _, _ := r.steps.d.Candidates.Candidate(context.Background(), r.u, roundAddress)
	for name, c := range map[string]struct {
		a    wsdomain.Activity
		want string
	}{
		"busy":        {wsdomain.Activity{Busy: true}, ""},
		"user active": {wsdomain.Activity{UserActive: true}, ""},
		"waiting":     {wsdomain.Activity{Waiting: true}, errWaiting.Error()},
	} {
		sc.ws.Activity = map[string]wsdomain.Activity{cand.Workspace.ID: c.a}
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
	sc.te.cfg.Rounds.PerPRPerDay = 1
	st := r.load(t)
	st.RecordFollowup(sc.ref.String(), round.Reviewed, "", testNow)
	r.save(t)
	refusal := r.check(t, automation.CheckOpts{Observe: false})
	if refusal != "per-PR daily cap reached (1)" {
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
		check         func(*testing.T, *round.Store, roundCandidate, string)
		kind          string
	}{
		"follow-up": {followupScenario, string(dintent.Review), roundFollowup, func(t *testing.T, st *round.Store, c roundCandidate, key string) {
			if st.Mark(key, round.Reviewed) != c.Fingerprint {
				t.Errorf("fingerprint %q, want %q", st.Mark(key, round.Reviewed), c.Fingerprint)
			}
		}, "follow-up"},
		"triage": {addressScenario, string(dintent.Address), "", func(t *testing.T, st *round.Store, c roundCandidate, key string) {
			if st.Mark(key, round.AddressedReviews) != c.Fingerprint || st.Mark(key, round.AddressedChecks) != c.ChecksHead {
				t.Errorf("fingerprint %q, checks head %q; want %+v", st.Mark(key, round.AddressedReviews), st.Mark(key, round.AddressedChecks), c)
			}
		}, "triage"},
	} {
		t.Run(name, func(t *testing.T) {
			sc := c.sc(t)
			r := newStartRig(t, sc, c.intent, c.round)
			cand, _, _ := r.steps.d.Candidates.Candidate(context.Background(), r.u, sc.env.startOf(r.st).candidate())
			r.load(t)
			if err := r.steps.Dismiss(context.Background(), r.st, r.u, r.why); err != nil {
				t.Fatal(err)
			}
			r.save(t)
			c.check(t, sc.te.loadRounds(t), cand, sc.ref.String())
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
	if got := offeredRounds(t, sc); len(got) != 0 {
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
				Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{r.st}, Approval: workflow.Approval{Default: workflow.OutcomeRun}}}
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
	cands := r.steps.d.Candidates.(fakeCandidates)
	key := sc.ref.String() + " " + roundAddress
	c := cands[key]
	c.Observed, c.Workspace = true, wsdomain.Workspace{}
	cands[key] = c
	sc.te.cfg.Rounds.PerPRPerDay = 1
	st := r.load(t)
	st.RecordFollowup(sc.ref.String(), round.Reviewed, "", testNow)
	r.save(t)
	refusal := r.check(t, automation.CheckOpts{Observe: false})
	if refusal != "per-PR daily cap reached (1)" {
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
	fc := fr.steps.d.Candidates.(fakeCandidates)
	fkey := fsc.ref.String() + " " + roundFollowup
	f := fc[fkey]
	f.Observed = true
	fc[fkey] = f
	fsc.te.cfg.Rounds.PerPRPerDay = 1
	fst := fr.load(t)
	fst.RecordFollowup(fsc.ref.String(), round.Reviewed, "", testNow)
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
	r.units.Set(r.u, "pr.address.reviewers", []string{"bob", "jussara-ai"})
	r.units.Set(r.u, "pr.address.checks", []string{})
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

func TestAUserReviewWithoutCapIsHeldByTheDailyCap(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	files := myReview(t)
	m := files["intents/my-review/intent.toml"]
	for _, key := range []string{"cap         = \"rounds\"\n", "round_kind  = \"review\"\n"} {
		if !strings.Contains(m, key) {
			t.Fatalf("the bundled review no longer sets %q", key)
		}
		m = strings.Replace(m, key, "", 1)
	}
	files["intents/my-review/intent.toml"] = m
	userProfile(t, sc.te, files)
	r := newStartRig(t, sc, "my-review", roundFollowup)
	sc.te.cfg.Rounds.PerPRPerDay = 1
	st := r.load(t)
	st.RecordFollowup(sc.ref.String(), round.Reviewed, "", testNow)
	r.save(t)
	if refusal := r.check(t, automation.CheckOpts{Observe: false}); refusal != "per-PR daily cap reached (1)" {
		t.Fatalf("refusal %q; broken %v", refusal, sc.env.loadedIntents().Broken)
	}
}
