package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/workspaces"
	wt "github.com/wloophq/wloop/internal/core/workspaces/workspacestest"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func turnStartWorkflow(name string) workflow.Workflow {
	return workflow.Workflow{Name: name, Provider: unit.GitHub, Slot: "pr", Observe: false,
		Trigger:  workflow.Trigger{Kind: workflow.TriggerSweep},
		Steps:    []workflow.Step{{Use: builtin.StepTurnStart, Args: map[string]any{"intent": "review", "stage": "follow-up"}}},
		Approval: workflow.Approval{Mode: workflow.ApprovalDelay, Window: time.Minute}}
}

func TestApplyConfigOverridesModeApprovalAndDisables(t *testing.T) {
	t.Parallel()
	off, window := false, config.Days(10*time.Minute)
	cfg := Defaults()
	cfg.Workflows = map[string]config.WorkflowConfig{
		"gone":    {Enabled: &off},
		"watched": {Mode: "observe"},
		"waits":   {Approval: &config.ApprovalConfig{Default: "delay", Window: &window}},
		"refused": {Approval: &config.ApprovalConfig{Default: "run", Window: &window}},
	}
	clean := workflow.Workflow{Name: "watched", Observe: false, Approval: workflow.Approval{Mode: workflow.ApprovalRun}}
	in := []workflow.Workflow{{Name: "gone", Observe: false}, clean, turnStartWorkflow("waits"), turnStartWorkflow("refused"), {Name: "kept", Observe: false}}
	out := applyConfig(in, cfg)
	var names []string
	for _, w := range out {
		names = append(names, w.Name)
	}
	if !slices.Equal(names, []string{"watched", "waits", "refused", "kept"}) {
		t.Fatalf("workflows %v", names)
	}
	if !out[0].Observe {
		t.Error("the config did not make it observe")
	}
	if a := out[1].Approval; a.Mode != workflow.ApprovalDelay || a.Window != 10*time.Minute {
		t.Errorf("approval %+v", a)
	}
	if ok, err := out[2].When.Match(workflow.Vars{}); ok || err == nil || !strings.Contains(err.Error(), "workflows.refused.approval") {
		t.Errorf("a window left on run matched: %v, %v", ok, err)
	}
	if in[1].Observe {
		t.Error("applyConfig changed the loaded workflow")
	}
}

func TestApplyConfigOnlyWhenNarrowsTheManifestsWhen(t *testing.T) {
	t.Parallel()
	when, err := expr.CompileWorkflow("pr.address.due", builtin.WorkflowEnv("pr"))
	if err != nil {
		t.Fatal(err)
	}
	address := turnStartWorkflow("address")
	address.When = when
	broken := turnStartWorkflow("broken")
	cfg := Defaults()
	cfg.Workflows = map[string]config.WorkflowConfig{
		"address": {OnlyWhen: `pr.visibility == "private"`},
		"broken":  {OnlyWhen: "pr.nope"},
	}
	out := applyConfig([]workflow.Workflow{address, broken}, cfg)
	vars := func(due bool, visibility string) workflow.Vars {
		return workflow.Vars{Facts: workflow.Facts{"pr.address.due": due, "pr.visibility": visibility}}
	}
	for _, c := range []struct {
		due        bool
		visibility string
		want       bool
	}{{true, "private", true}, {true, "public", false}, {true, "", false}, {false, "private", false}} {
		if got, err := out[0].When.Match(vars(c.due, c.visibility)); err != nil || got != c.want {
			t.Errorf("due %v, %q: %v, %v; want %v", c.due, c.visibility, got, err, c.want)
		}
	}
	if reads := out[0].When.Reads(); !slices.Contains(reads, "pr.address.due") || !slices.Contains(reads, "pr.visibility") {
		t.Errorf("reads %v", reads)
	}
	if ok, err := out[1].When.Match(vars(true, "private")); ok || err == nil || !strings.Contains(err.Error(), "workflows.broken.only_when") {
		t.Errorf("a broken only_when matched: %v, %v", ok, err)
	}
}

// countingRounds counts the round state loads and saves.
type countingRounds struct {
	Rounds
	mu           sync.Mutex
	loads, saves int
}

func (c *countingRounds) Load(ctx context.Context) (*round.Store, error) {
	c.mu.Lock()
	c.loads++
	c.mu.Unlock()
	return c.Rounds.Load(ctx)
}

func (c *countingRounds) Save(ctx context.Context, st *round.Store) error {
	c.mu.Lock()
	c.saves++
	c.mu.Unlock()
	return c.Rounds.Save(ctx, st)
}

func TestOneStateLoadPerSweep(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	if err := r.fetches.Mark(context.Background(), "ws1", testNow); err != nil {
		t.Fatal(err)
	}
	counted := &countingRounds{Rounds: r.te.Rounds}
	r.s.d.Rounds = counted
	r.units.Set(r.u, "workspace.fingerprint", "ws1:c0ffee")
	eng := &automation.Engine{
		Workflows: func() []workflow.Workflow {
			return []workflow.Workflow{{Name: "workspace-review-finished", Provider: unit.Workspace, Slot: "workspace", Observe: false,
				Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{wfStep("workspace.remove")},
				Approval: workflow.Approval{Mode: workflow.ApprovalRun}}}
		},
		Units: r.units, Steps: r.s, Ledger: automationtest.NewLedger(), Clock: clockFunc(r.te.Env.Now), State: r.s.states(),
	}
	if _, _, err := eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	if counted.loads != 1 || counted.saves != 1 {
		t.Fatalf("loads %d, saves %d", counted.loads, counted.saves)
	}
	if !slices.Equal(r.mgr.Removed(), []string{"ws1"}) {
		t.Fatalf("removed %v", r.mgr.Removed())
	}
	if !r.te.loadRounds(t).FirstRoundSkipped("acme/api#7") {
		t.Fatal("the removal's SkipFirstRound did not land in the saved state")
	}
}

func TestPendingRunsKeepTheGateMeterScope(t *testing.T) {
	t.Parallel()
	units := automationtest.NewUnits()
	u := unit.Unit{Provider: unit.Session, Key: "t1"}
	units.Put(u, workflow.Facts{"session.fingerprint": "t1:a"})
	clock := automationtest.NewClock(testNow)
	steps := automationtest.NewSteps(map[string]workflow.Reach{"session.close": workflow.ReachLocal})
	ledger := automationtest.NewLedger()
	eng := meteredEngine{&automation.Engine{
		Workflows: func() []workflow.Workflow {
			return []workflow.Workflow{{Name: "session-done", Provider: unit.Session, Slot: "session", Observe: false,
				Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{steps.Step("session.close")},
				Approval: workflow.Approval{Mode: workflow.ApprovalDelay, Window: time.Minute}}}
		},
		Units: units, Steps: steps, Ledger: ledger, Clock: clock,
	}}
	reg := meter.NewRegistry(nil)
	ctx := meter.WithRegistry(context.Background(), reg)
	jctx, job := meter.Start(ctx, meter.Job("sweep"))
	if _, _, err := eng.Sweep(jctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	job.End()
	clock.Advance(2 * time.Minute)
	if err := eng.DueRun(ctx); err != nil {
		t.Fatal(err)
	}
	if len(steps.Ran()) != 1 {
		t.Fatalf("ran %v", steps.Ran())
	}
	units.Set(u, "session.fingerprint", "t1:b")
	if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	items, err := ledger.Items(ctx, automation.Filter{Decisions: []workflow.Decision{workflow.DecisionPending}})
	if err != nil || len(items) != 1 {
		t.Fatalf("pending %v %v", items, err)
	}
	if _, err := eng.Act(ctx, items[0].ID, workflow.ActionApply, ""); err != nil {
		t.Fatal(err)
	}
	runs := map[string]int64{}
	for _, tot := range reg.Totals() {
		runs[tot.Name] = tot.Runs
	}
	if runs[meter.Loop("gate")] != 2 || runs[meter.Job("sweep")+"/lockwait"] != 1 {
		t.Fatalf("scopes %v", runs)
	}
}

func TestPendingRoundsServeTheReadModel(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ledger := automationtest.NewLedger()
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	sess := unit.Unit{Provider: unit.Session, Key: "t1"}
	deadline := testNow.Add(5 * time.Minute)
	items := []workflow.Item{
		{ID: "a", Workflow: "review-follow-up", Unit: pr, Decision: workflow.DecisionPending, Reason: "runs when its window ends", Default: workflow.OutcomeRun,
			Deadline: deadline, At: testNow, Steps: []workflow.Step{{Use: builtin.StepTurnStart, Args: map[string]any{"intent": string(dintent.Review), "stage": "follow-up"}}}},
		{ID: "b", Workflow: "session-manual", Unit: sess, Decision: workflow.DecisionPending, Default: workflow.OutcomeWait, At: testNow,
			Steps: []workflow.Step{wfStep("session.close")}},
	}
	if err := ledger.Save(context.Background(), automation.PlanWrite{Workflows: []string{"review-follow-up", "session-manual"}, Items: items}); err != nil {
		t.Fatal(err)
	}
	te.engine = &automation.Engine{Ledger: ledger}
	got, err := te.pendingRounds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[unit.Unit]round.Pending{pr: {Intent: "follow-up", Reason: "runs when its window ends", Since: testNow, Deadline: deadline}}
	if len(got) != 1 || got[pr] != want[pr] {
		t.Fatalf("pending %+v", got)
	}
}

func TestUnitsRouteByProvider(t *testing.T) {
	t.Parallel()
	gh, sess := automationtest.NewUnits(), automationtest.NewUnits()
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	s := unit.Unit{Provider: unit.Session, Key: "t1"}
	gh.Put(pr, workflow.Facts{"pr.repo": "acme/api"})
	sess.Put(s, workflow.Facts{"session.repo": ""})
	gh.SetDue(testNow.Add(time.Hour))
	sess.SetDue(testNow.Add(time.Minute))
	sess.SetMoved(s)
	set := newUnitSet(map[unit.ProviderID]automation.Units{unit.GitHub: gh, unit.Session: sess})
	if us, err := set.List(context.Background(), unit.Session); err != nil || !slices.Equal(us, []unit.Unit{s}) {
		t.Fatalf("list %v %v", us, err)
	}
	if f, err := set.Facts(context.Background(), pr); err != nil || f["pr.repo"] != "acme/api" {
		t.Fatalf("facts %v %v", f, err)
	}
	if _, err := set.List(context.Background(), "slack"); err == nil {
		t.Fatal("an unknown provider listed")
	}
	if got := set.Due(); !got.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("due %v", got)
	}
	if moved, err := set.Moved(context.Background(), []unit.Unit{pr, s}); err != nil || !slices.Equal(moved, []unit.Unit{s}) {
		t.Fatalf("moved %v %v", moved, err)
	}
	var refreshed []string
	gh.OnRefresh(func(context.Context) error { refreshed = append(refreshed, "github"); return nil })
	sess.OnRefresh(func(context.Context) error { refreshed = append(refreshed, "session"); return nil })
	if err := set.Refresh(context.Background()); err != nil || !slices.Equal(refreshed, []string{"github", "session"}) {
		t.Fatalf("refreshed %v %v", refreshed, err)
	}
	st := round.New()
	var settled *round.Store
	gh.OnSettle(func(_ context.Context, s automation.State) error {
		settled = s.(interface{ Rounds() *round.Store }).Rounds()
		return nil
	})
	if err := set.Settle(context.Background(), &roundState{st: st}); err != nil || settled != st {
		t.Fatalf("settled %p, want %p: %v", settled, st, err)
	}
	sess.SetSeen(7)
	if got := set.Seen(); got != 7 {
		t.Fatalf("seen %d", got)
	}
}

func TestNewEngineAssemblesOverTheStore(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "e.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	eng := te.newEngine(engineDeps{DB: db})
	if eng != te.engine {
		t.Fatal("the engine is not the Env's")
	}
	if _, err := eng.Evaluate(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestTheGithubModuleOffersTheRounds(t *testing.T) {
	t.Parallel()
	p := factsgithub.New(factsgithub.Deps{})
	if _, ok := candidatesOf([]source.Module{{ID: unit.GitHub, Facts: p}}).(githubCandidates); !ok {
		t.Fatal("the github facts provider does not offer its rounds")
	}
	if candidatesOf(nil) != nil {
		t.Fatal("no module offers rounds")
	}
}

// threeProviders is a followup scenario whose daemon keeps a complete
// workspace snapshot and a session in ws-rev, and a github module that
// offers #7.
func threeProviders(t *testing.T) (sweepCase, engineDeps) {
	t.Helper()
	sc := followupScenario(t)
	sc.te.WS = workspace.NewSet(workspace.NewCached(sc.ws, sc.ws, 2*time.Minute))
	if err := sc.te.reg.SaveSession(context.Background(), dturn.Session{ID: "term-rev", WorkspaceID: "ws-rev", Worktree: "/wt/rev", StartedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	gh := automationtest.NewUnits()
	gh.Put(sc.ref.Unit(), workflow.Facts{"pr.repo": "acme/api", "pr.fingerprint": "fp"})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "e.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return sc, engineDeps{DB: db, Modules: []source.Module{{ID: unit.GitHub, Facts: gh}}}
}

func everyUnit(p unit.ProviderID, slot string) workflow.Workflow {
	return workflow.Workflow{Name: "watch-" + slot, Provider: p, Slot: slot, Observe: true,
		Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{wfStep("notify.self", "text", "seen")},
		Approval: workflow.Approval{Mode: workflow.ApprovalRun}}
}

func TestASweepThroughNewEngineListsEveryProvider(t *testing.T) {
	t.Parallel()
	sc, d := threeProviders(t)
	eng := sc.env.newEngine(d)
	eng.Workflows = func() []workflow.Workflow {
		return []workflow.Workflow{everyUnit(unit.GitHub, "pr"), everyUnit(unit.Session, "session"), everyUnit(unit.Workspace, "workspace")}
	}
	if _, _, err := eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	items, err := eng.Ledger.Items(context.Background(), automation.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[unit.ProviderID]bool{}
	for _, it := range items {
		seen[it.Unit.Provider] = true
	}
	if !seen[unit.GitHub] || !seen[unit.Session] || !seen[unit.Workspace] {
		t.Fatalf("items %+v", items)
	}
}

func TestThePlanEngineKeepsItsOwnSeen(t *testing.T) {
	t.Parallel()
	sc, d := threeProviders(t)
	sc.env.changes = change.NewLog(change.DefaultWindow, nil)
	eng := sc.env.newEngine(d)
	plan := sc.env.planEngine(d)
	if eng == plan || sc.env.engine != eng {
		t.Fatal("the plan engine is the sweep's")
	}
	w := func() []workflow.Workflow { return []workflow.Workflow{everyUnit(unit.Workspace, "workspace")} }
	eng.Workflows, plan.Workflows = w, w
	if _, _, err := eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	seen := eng.Seen()
	sc.env.changes.Note(port.TopicAgents)
	if _, err := plan.Evaluate(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := eng.Units.(*unitSet).Seen(); got != seen {
		t.Fatalf("a plan moved the sweep's Seen from %d to %d", seen, got)
	}
}

func TestTurnStartWindowsComeFromTheManifest(t *testing.T) {
	t.Parallel()
	if w := applyConfig([]workflow.Workflow{turnStartWorkflow("review-follow-up")}, Defaults())[0]; w.Approval.Window != time.Minute {
		t.Fatalf("window %v", w.Approval.Window)
	}
	if w := applyConfig([]workflow.Workflow{turnStartWorkflow("review-follow-up")}, withoutWindows(Defaults()))[0]; w.Approval.Window != 0 {
		t.Fatalf("window with approval.mode run %v", w.Approval.Window)
	}
}

func TestGuidanceAGuidedRunDidNotUseIsKeptUnlessNewerIsStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rec := &memRecorder{}
	g := recGuidance{&Env{Rec: rec}}
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	if err := rec.KeepNextGuidance(ctx, ref, "check the migration"); err != nil {
		t.Fatal(err)
	}
	taken, err := g.Take(ctx, ref.Unit())
	if err != nil || taken != "check the migration" {
		t.Fatalf("Take = %q, %v", taken, err)
	}
	if err := g.Keep(ctx, ref.Unit(), taken); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Peek(ctx, ref.Unit()); got != "check the migration" {
		t.Fatalf("kept %q", got)
	}
	if _, err := g.Take(ctx, ref.Unit()); err != nil {
		t.Fatal(err)
	}
	if err := rec.KeepNextGuidance(ctx, ref, "only the tests"); err != nil {
		t.Fatal(err)
	}
	if err := g.Keep(ctx, ref.Unit(), "check the migration"); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Peek(ctx, ref.Unit()); got != "only the tests" {
		t.Errorf("Keep replaced newer guidance: %q", got)
	}
}

func TestNoRemovalMatchesAWorkspaceWhosePathIsGone(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	cfg, err := te.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	month := 30 * 24 * time.Hour
	removals := 0
	for _, w := range te.loadedIntents().Workflows {
		if !slices.ContainsFunc(w.Steps, func(st workflow.Step) bool { return st.Use == "workspace.remove" }) {
			continue
		}
		removals++
		for _, role := range []string{"review", "author"} {
			facts := workflow.Facts{"workspace.main": false, "workspace.pr": "", "workspace.sessions": 0, "workspace.quiet_for": month,
				"workspace.role": role, "workspace.pr_state": "merged", "workspace.pr_finished_for": month, "workspace.temporary": true,
				"workspace.duplicate": true, "workspace.path_exists": true}
			vars := workflow.Vars{Facts: facts, Trigger: workflow.Facts{"trigger.kind": "sweep"}, Config: configFacts(cfg), Now: te.Now()}
			present, err := w.When.Match(vars)
			if err != nil {
				t.Fatalf("%s: %v", w.Name, err)
			}
			facts["workspace.path_exists"] = false
			if gone, err := w.When.Match(vars); err != nil || (present && gone) {
				t.Errorf("%s matches a %s workspace whose path is gone (%v)", w.Name, role, err)
			}
		}
	}
	if removals != 5 {
		t.Fatalf("%d bundled workflows remove a workspace, want 5", removals)
	}
}

// appliedEngine is a sweep engine over a store, running one apply workflow
// of step on the workspace ws1, whose path is gone; u is its unit.
func appliedEngine(t *testing.T, step workflow.Step, pub port.Publisher) (*testEnv, *sqlite.DB, *automation.Engine, unit.Unit) {
	t.Helper()
	gone := filepath.Join(t.TempDir(), "gone")
	te := newTestEnv(t, &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "ws1", RepoID: "r1", Path: gone, Branch: "b", HostID: wsdomain.LocalHost}}})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "e.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	u := wsdomain.UnitOf(gone)
	facts := automationtest.NewUnits()
	facts.Put(u, workflow.Facts{"workspace.path_exists": false})
	eng := te.newEngine(engineDeps{DB: db, WorkspaceFacts: facts, Pub: pub})
	eng.Workflows = func() []workflow.Workflow {
		return []workflow.Workflow{{Name: "gone", Provider: unit.Workspace, Slot: "workspace", Observe: false,
			Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{step}, Approval: workflow.Approval{Mode: workflow.ApprovalRun}}}
	}
	return te, db, eng, u
}

func ranOnce(t *testing.T, db *sqlite.DB, eng *automation.Engine) {
	t.Helper()
	if _, _, err := eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	evs, err := db.Events().Since(context.Background(), 0, []string{workflow.KindStepRan})
	if err != nil || len(evs) != 1 {
		t.Fatalf("step_ran events %+v: %v", evs, err)
	}
}

func TestAnUnregisterThroughTheEngineRecordsItsRunAndItsEvent(t *testing.T) {
	t.Parallel()
	te, db, eng, u := appliedEngine(t, wfStep("workspace.unregister"), nil)
	ranOnce(t, db, eng)
	if list, _ := te.Env.WS.List(context.Background()); len(list) != 0 {
		t.Fatalf("still registered: %+v", list)
	}
	evs, err := db.Events().Since(context.Background(), 0, []string{workspaces.KindUnregistered})
	if err != nil || len(evs) != 1 || evs[0].Unit != u {
		t.Fatalf("unregistered events %+v: %v", evs, err)
	}
}

func TestNotifySelfThroughTheEngineReachesTheNotices(t *testing.T) {
	t.Parallel()
	feed := &noticeFeed{}
	var got []api.Notice
	stop := feed.Follow(func(n api.Notice) { got = append(got, n) })
	defer stop()
	te, db, eng, u := appliedEngine(t, wfStep("notify.self"), feed)
	besideWorkflow(t, te, "gone", map[string]string{"text.md.tmpl": "look at it"})
	ranOnce(t, db, eng)
	if len(got) != 1 || got[0].Text != "look at it" || got[0].Workflow != "gone" || got[0].Unit != u || got[0].ID == "" {
		t.Fatalf("notices %+v", got)
	}
	evs, err := db.Events().Since(context.Background(), 0, []string{kindNotifySelf})
	if err != nil || len(evs) != 1 || evs[0].Data != "look at it" {
		t.Fatalf("notify events %+v: %v", evs, err)
	}
}

func TestThePlanEnginesWorkspaceFactsAreASibling(t *testing.T) {
	t.Parallel()
	sc, _ := threeProviders(t)
	f := sc.env.workspaceFacts()
	s := siblingFacts(f)
	if f == nil || s == nil || s == f {
		t.Fatalf("sibling %v of %v", s, f)
	}
	if siblingFacts(automationtest.NewUnits()) != nil {
		t.Fatal("a sibling of another provider")
	}
}

func TestApplyConfigSetsAWorkflowsDraftApproval(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Workflows = map[string]config.WorkflowConfig{"drafts": {DraftApproval: &config.DraftApprovalConfig{Mode: "delay", Window: config.Days(5 * time.Minute)}}}
	manifest := draft.Fixed(draft.Approval{Mode: draft.Wait})
	out := applyConfig([]workflow.Workflow{{Name: "drafts", Slot: "slack", Observe: false, DraftApproval: manifest}}, cfg)
	if a := out[0].DraftApproval; a == nil || a.Default.Mode != draft.Delay || a.Default.Window != 5*time.Minute {
		t.Fatalf("draft approval %+v", a)
	}
	if manifest.Default.Mode != draft.Wait {
		t.Error("applyConfig changed the manifest's approval")
	}
}

func TestConfigFactsCarryTheProposalSettings(t *testing.T) {
	t.Parallel()
	f := configFacts(Defaults())
	if f["config.gtd.stale_waiting_days"] != 2 || f["config.drafts.repeat_after"] != 24*time.Hour || f["config.turns.per_day"] != 20 {
		t.Fatalf("%v", f)
	}
}

func TestApplyConfigOnlyWhenReadsTheBriefExtras(t *testing.T) {
	t.Parallel()
	extras := func(p unit.ProviderID) expr.Env {
		if p != unit.GitHub {
			return nil
		}
		return builtin.ExtraFacts([]jsonschema.Property{{Name: "approach", Type: "string", Enum: []string{"small", "big"}}})
	}
	cfg := Defaults()
	cfg.Workflows = map[string]config.WorkflowConfig{"address": {OnlyWhen: `brief.extra.approach == "small"`}}
	w := applyConfigWith([]workflow.Workflow{turnStartWorkflow("address")}, cfg, extras)[0]
	for approach, want := range map[string]bool{"small": true, "big": false} {
		if got, err := w.When.Match(workflow.Vars{Facts: workflow.Facts{"brief.extra.approach": approach}}); err != nil || got != want {
			t.Errorf("approach %q: %v, %v; want %v", approach, got, err, want)
		}
	}
}
