package cli

import (
	"context"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/fakethread"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/profiles"
)

var thread2 = unit.Unit{Provider: ingesttest.Provider, Key: "1700000000.2"}

// implementIntent is a user intent on threads that runs in the repository
// its workspace table names.
const implementIntent = `label = "Implement"
summary = "Implement what a thread asks for"
workspace = { repo = "acme/api" }
provider = "thread"
session = { strategy = "new", title = "implement" }

[headline]
resumed = "implement {{.unit.ref}}"
[[headline.new]]
line = 'Implement {{.unit.ref}}, {{template "started_by" .}}.'

[record]
states = ["done", "asked", "failed"]
`

const implementBody = `# {{.Headline}}
{{template "items" .}}
{{template "record" .}}`

const implementWorkflow = "implement-requests"

// unitStartRig is turn.start of the user intent implement on threads, over
// a real store and a fake manager that knows acme/api.
type unitStartRig struct {
	te    *testEnv
	m     *fake.Manager
	units *automationtest.Units
	steps *stepSet
	st    workflow.Step
	why   workflow.Why
}

func newUnitStartRig(t *testing.T, intentToml string) *unitStartRig {
	t.Helper()
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api", DefaultBranch: "main"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	te := newTestEnv(t, m)
	te.Rec = storeRecorder{te.Store}
	saved := plugins
	plugins = append(slices.Clone(plugins), threadModule(thread1))
	t.Cleanup(func() { plugins = saved })
	te.payloads = map[unit.ProviderID]turn.PayloadReader{ingesttest.Provider: fakethread.Payloads{}}
	userProfile(t, te, map[string]string{"intents/implement/intent.toml": intentToml, "intents/implement/instructions.md.tmpl": implementBody})
	te.Env.serveTurnLedger(te.db, te.cfg, t.Logf)
	kind, sum := items.Request, "Add a retry to the export job"
	if _, err := te.Env.itemsLedger(te.db, te.cfg, t.Logf).Apply(context.Background(), items.Extracted, []items.Op{{Op: items.OpCreate, Unit: thread1, Item: "i1",
		Anchor: unit.Anchor{Ref: "msg:1"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}}}); err != nil {
		t.Fatal(err)
	}
	units := automationtest.NewUnits()
	for _, u := range []unit.Unit{thread1, thread2} {
		units.Put(u, workflow.Facts{"brief.kind": "request"})
	}
	return &unitStartRig{te: te, m: m, units: units,
		steps: newStepSet(te.Env, stepDeps{Units: units, Rounds: te.Rounds, Turns: te.db}),
		st:    workflow.Step{Use: builtin.StepTurnStart, Args: map[string]any{"intent": "implement"}},
		why:   workflow.Why{Workflow: implementWorkflow, Trigger: workflow.TriggerSweep}}
}

func (r *unitStartRig) book(t *testing.T, u unit.Unit) dturn.Book {
	t.Helper()
	books, err := r.te.repo.Books(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return books[u]
}

// created says whether a worktree of acme/api was created.
func (r *unitStartRig) created() bool {
	return slices.ContainsFunc(r.m.WorkspaceList, func(w wsdomain.Workspace) bool { return w.RepoID == "r1" && !w.IsMain })
}

func TestTurnStartLaunchesASlackIntentInTheRepositoryItNames(t *testing.T) {
	r := newUnitStartRig(t, implementIntent)
	ctx := context.Background()
	if got, err := r.steps.Check(ctx, r.st, thread1, r.why, automation.CheckOpts{Observe: false}); err != nil || got != "" {
		t.Fatalf("check %q %v", got, err)
	}
	got, err := r.steps.Run(ctx, r.st, thread1, r.why, automation.RunOpts{})
	if err != nil || got != "started a implement turn" {
		t.Fatalf("run %q %v\n%s", got, err, r.te.err)
	}
	if !r.created() || len(r.m.Prompts) != 1 {
		t.Fatalf("writes %q, prompts %+v", r.m.Writes, r.m.Prompts)
	}
	b := r.book(t, thread1)
	if len(b.Turns) != 1 || b.Turns[0].Trigger != dturn.Automated || b.Turns[0].Workflow != implementWorkflow || b.Turns[0].Intent != "implement" {
		t.Fatalf("turns %+v", b.Turns)
	}
	if text := storedText(t, r.te, b.Turns[0].ID); !strings.Contains(text, "Add a retry to the export job") {
		t.Fatalf("the thread's items are not in the instructions:\n%s", text)
	}
	again, err := r.steps.Check(ctx, r.st, thread1, r.why, automation.CheckOpts{Observe: false})
	if err != nil || !strings.HasPrefix(again, "its implement turn is ") {
		t.Fatalf("a second start on the open turn: %q %v", again, err)
	}
}

func TestARepositoryFromAnInputIsNeverTakenByAWorkflow(t *testing.T) {
	r := newUnitStartRig(t, strings.Replace(implementIntent, `workspace = { repo = "acme/api" }`, `workspace = { repo_input = "repo" }`, 1)+`
[inputs.repo]
type = "text"
flag = "--repo"
help = "owner/name"
`)
	got, err := r.steps.Check(context.Background(), r.st, thread1, r.why, automation.CheckOpts{Observe: false})
	if err != nil || !strings.Contains(got, "input repo") {
		t.Fatalf("check %q %v", got, err)
	}
	if _, err := r.steps.Run(context.Background(), r.st, thread1, r.why, automation.RunOpts{}); err == nil || r.created() {
		t.Fatalf("run %v, writes %q", err, r.m.Writes)
	}
	d, err := r.te.intentDef("implement")
	if err != nil {
		t.Fatal(err)
	}
	run := unitRun{def: d, cfg: r.te.cfg, r: turn.Request{Intent: "implement", Trigger: turn.ByHand, Unit: thread1, Inputs: map[string]any{"repo": "acme/api"}}}
	if err := r.te.runOnUnit(context.Background(), run); err != nil {
		t.Fatalf("%v\n%s", err, r.te.err)
	}
	if !r.created() {
		t.Fatalf("writes %q", r.m.Writes)
	}
	run.r.Unit, run.r.Inputs = thread2, map[string]any{"repo": "evil/elsewhere"}
	if err := r.te.runOnUnit(context.Background(), run); err == nil || !strings.Contains(err.Error(), "does not know") {
		t.Fatalf("a repository the manager does not know: %v", err)
	}
}

// engine is an engine sweeping w over the rig's units with its steps.
func (r *unitStartRig) engine(w workflow.Workflow) *automation.Engine {
	return &automation.Engine{Workflows: func() []workflow.Workflow { return []workflow.Workflow{w} }, Units: r.units, Steps: r.steps,
		Ledger: automationtest.NewLedger(), Clock: automationtest.NewClock(r.te.Now()), Config: func() workflow.Facts { return configFacts(r.te.cfg) }}
}

func (r *unitStartRig) sweep(t *testing.T, eng *automation.Engine) {
	t.Helper()
	if _, _, err := eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	if err := eng.DueRun(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTheWorkflowLimitAndTheAutomatedBudgetStopTheNextTurn(t *testing.T) {
	w := workflow.Workflow{Name: implementWorkflow, Provider: ingesttest.Provider, Slot: string(ingesttest.Provider), Trigger: workflow.Trigger{Kind: workflow.TriggerSweep},
		When: automationtest.MatchFunc(func(workflow.Vars) bool { return true }), Steps: []workflow.Step{{Use: builtin.StepTurnStart, Args: map[string]any{"intent": "implement"}}},
		Observe: false, Approval: workflow.Approval{Mode: workflow.ApprovalRun}, Limits: workflow.Limits{PerDay: 1}}

	r := newUnitStartRig(t, implementIntent)
	eng := r.engine(w)
	r.sweep(t, eng)
	if n := len(r.m.Prompts); n != 1 {
		t.Fatalf("limits.per_day 1 started %d turns", n)
	}
	refusedWith(t, eng, "limit per_day 1 reached")

	r = newUnitStartRig(t, implementIntent)
	r.te.cfg.Turns = config.Turns{PerDay: 1}
	w.Limits.PerDay = 5
	eng = r.engine(w)
	r.sweep(t, eng)
	if n := len(r.m.Prompts); n != 1 {
		t.Fatalf("turns.per_day 1 started %d turns", n)
	}
	refusedWith(t, eng, "turns.per_day reached (1)")
}

// refusedWith fails unless an item of the implement workflow says why.
func refusedWith(t *testing.T, eng *automation.Engine, why string) {
	t.Helper()
	its, err := eng.Ledger.Items(context.Background(), automation.Filter{Workflow: implementWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(its, func(it workflow.Item) bool { return strings.Contains(it.Reason, why) }) {
		t.Fatalf("no item says %q: %+v", why, its)
	}
}

func TestTheAutomatedBudgetCountsTheDayOutsideTheRoundProviders(t *testing.T) {
	r := newUnitStartRig(t, implementIntent)
	ctx := context.Background()
	at := r.te.Now()
	for i, u := range []unit.Unit{thread1, ghUnit(7)} {
		if _, err := r.te.Turns.Send(ctx, turns.SendRequest{Unit: u, Intent: "implement", Turn: "t" + string(rune('a'+i)), Trigger: dturn.Automated, Records: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.te.Turns.Send(ctx, turns.SendRequest{Unit: thread2, Intent: "implement", Turn: "tc", Trigger: dturn.ByHand, Records: true}); err != nil {
		t.Fatal(err)
	}
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	n, err := r.te.db.AutomatedTurns(ctx, day, builtin.RoundProviders)
	if err != nil || n != 1 {
		t.Fatalf("today %d %v, want the thread's automated turn alone", n, err)
	}
	if n, err := r.te.db.AutomatedTurns(ctx, at.Add(time.Second), builtin.RoundProviders); err != nil || n != 0 {
		t.Fatalf("after them %d %v", n, err)
	}
}

func TestRoundsReadTheManifestKeys(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		args  startArgs
		round string
		kind  stepKind
	}{
		{"triage under another name", startArgs{intent: "triage", def: dintent.Def{RoundKind: builtin.RoundKindAddress}}, roundAddress, stepAddress},
		{"a review's first round", startArgs{intent: "critique", round: roundFirst, def: dintent.Def{RoundKind: builtin.RoundKindReview}}, roundFirst, stepFirstRound},
		{"a review's follow-up", startArgs{intent: "critique", def: dintent.Def{RoundKind: builtin.RoundKindReview}}, roundFollowup, stepFollowup},
		{"the bundled review's name without its role", startArgs{intent: string(dintent.Review)}, "", stepFollowup},
	} {
		if got := c.args.candidate(); got != c.round {
			t.Errorf("%s: round %q, want %q", c.name, got, c.round)
		}
		if c.args.kind() != c.kind {
			t.Errorf("%s: kind %v", c.name, c.args.kind())
		}
	}
	addr := startPlan{args: startArgs{def: dintent.Def{RoundKind: builtin.RoundKindAddress}}, facts: workflow.Facts{"pr.address.reviewers": []string{"bob"}}}
	if addr.address() == nil || (startPlan{args: startArgs{def: dintent.Def{RoundKind: builtin.RoundKindReview}}}).address() != nil {
		t.Error("the address reason follows the role's round")
	}
}

// nudgeWithoutRefuse is the bundled nudge's manifest with no [[refuse]].
func nudgeWithoutRefuse(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(profiles.Default, "default/intents/nudge/intent.toml")
	if err != nil {
		t.Fatal(err)
	}
	m := string(b)
	for strings.Contains(m, "[[refuse]]") {
		i := strings.Index(m, "[[refuse]]")
		end := strings.Index(m[i:], "}}\"\n")
		m = m[:i] + m[i+end+len("}}\"\n"):]
	}
	return m
}

func TestANudgeWithoutRefuseIgnoresTheNudgeRefusal(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.units.Set(r.u, "pr.nudge.refusal", "it waits on you")
	if got := r.check(t, automation.CheckOpts{Observe: false}); got != "it waits on you" {
		t.Fatalf("the bundled nudge: %q", got)
	}
	userProfile(t, r.sc.te, map[string]string{"intents/nudge/intent.toml": nudgeWithoutRefuse(t)})
	if got := r.check(t, automation.CheckOpts{Observe: false}); got == "it waits on you" {
		t.Fatal("a nudge whose manifest has no [[refuse]] read the refusal")
	}
}

func TestRefuseIsFirstMatch(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.units.Set(r.u, "pr.nudge.refusal", "it waits on you")
	r.units.Set(r.u, "pr.mute.refusal", "muted until its state changes")
	if got := r.check(t, automation.CheckOpts{Observe: false}); got != "it waits on you" {
		t.Fatalf("both hold: %q, want the first refusal's", got)
	}
	r.units.Set(r.u, "pr.nudge.refusal", "")
	if got := r.check(t, automation.CheckOpts{Observe: false}); got != "muted until its state changes" {
		t.Fatalf("the mute alone: %q", got)
	}
}

func TestMuteRefusesOnlyWithAnExplicitRefuse(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.units.Set(r.u, "pr.mute.refusal", "muted until its state changes")
	userProfile(t, r.sc.te, map[string]string{"intents/nudge/intent.toml": nudgeWithoutRefuse(t)})
	if got := r.check(t, automation.CheckOpts{Observe: false}); got == "muted until its state changes" {
		t.Fatal("an intent with no [[refuse]] read the mute")
	}
}

func TestTheNudgeSaysByHandOnlyWhenAPersonStartedIt(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	for trig, want := range map[turn.Trigger]string{turn.ByHand: "The user asked for this nudge by hand.", turn.Automated: "A workflow of the user's started this nudge."} {
		req := turn.Request{Intent: turn.Intent(dintent.Nudge), Trigger: trig, Unit: r7.Unit(), Payload: &turn.PR{Title: "Batch export"},
			Inputs: map[string]any{"situation": nudgeLine, "who": []string{"ana"}}}
		_, out, err := te.renderTurn(context.Background(), te.cfg, req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Text, want) || (trig == turn.Automated && strings.Contains(out.Text, "by hand")) {
			t.Errorf("%v:\n%s", trig, out.Text)
		}
	}
}

// myReview is the bundled review intent under another name, holding its
// role, without what only one intent, or only a bundled one, may hold.
func myReview(t *testing.T) map[string]string {
	t.Helper()
	return bundledIntentAs(t, "review", "my-review")
}

// bundledIntentAs is the files of the bundled intent from as a user's
// intent named as.
func bundledIntentAs(t *testing.T, from, as string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range []string{"intent.toml", "instructions.md.tmpl"} {
		b, err := fs.ReadFile(profiles.Default, "default/intents/"+from+"/"+name)
		if err != nil {
			t.Fatal(err)
		}
		files["intents/"+as+"/"+name] = string(b)
	}
	return files
}

func TestAUserReviewIntentWithTheRoleReplacesTheBundledOne(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	userProfile(t, sc.te, myReview(t))
	if got := sc.env.roleIntent(builtin.RoleReview); got != "my-review" {
		t.Fatalf("the review role is held by %q; broken %v", got, sc.env.loadedIntents().Broken)
	}
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	b := sc.te.unitBook(t, sc.ref.Unit())
	if len(b.Turns) != 1 || b.Turns[0].Intent != "my-review" || b.Turns[0].Role != builtin.RoleReview {
		t.Fatalf("turns %+v", b.Turns)
	}
	if sc.env.blockedIntent(inventory.StartBlock{Round: "follow-up"}) != "my-review" {
		t.Fatal("a held follow-up is not my-review's")
	}
}

func TestTheRoleHolderAnswersTurnStartsGuards(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	files := myReview(t)
	files["intents/my-review/intent.toml"] += "\n[[refuse]]\nwhen = 'pr.repo == \"acme/api\"'\nwhy  = \"not on acme\"\n"
	userProfile(t, sc.te, files)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	if got := r.check(t, automation.CheckOpts{}); got != "not on acme" {
		t.Fatalf("the holder's [[refuse]]: %q", got)
	}
	r.units.Set(r.u, "pr.repo", "acme/web")
	r.rec.questions = map[github.Ref]map[string]bool{sc.ref: {"my-review": true}}
	if got := r.check(t, automation.CheckOpts{}); got != "its my-review turn asks you something" {
		t.Fatalf("the holder's open question: %q", got)
	}
	r.rec.questions = nil
	r.briefs.intakes = []intake.Intake{{Unit: r.u, Intent: "my-review"}}
	if got := r.check(t, automation.CheckOpts{}); got != "an intake of yours asks for it" {
		t.Fatalf("the holder's open intake: %q", got)
	}
}

func TestAWorkflowLaunchingARoleLaunchesItsHolder(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	userProfile(t, r.sc.te, bundledIntentAs(t, "nudge", "my-nudge"))
	if a := r.sc.env.startOf(r.st, r.u.Provider); a.intent != "my-nudge" || a.def.Name != "my-nudge" {
		t.Fatalf("the nudge step starts %q (%q)", a.intent, a.def.Name)
	}
	r.units.Set(r.u, "pr.nudge.refusal", "it waits on you")
	if got := r.check(t, automation.CheckOpts{}); got != "it waits on you" {
		t.Fatalf("the holder's refusal: %q", got)
	}
}

func TestEveryAutomatedTurnSpendsThePerUnitBudget(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	files := myReview(t)
	m := files["intents/my-review/intent.toml"]
	files["intents/my-review/intent.toml"] = regexp.MustCompile(`(?m)^effects\s*=.*$`).ReplaceAllString(m, "")
	userProfile(t, sc.te, files)
	sc.te.cfg.Turns.PerUnitPerDay = 1
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := sc.te.loadRounds(t).Days[round.DayKey(testNow)].PerPR[sc.ref.String()]; got != 1 {
		t.Fatalf("%d turns counted, want 1", got)
	}
	if got := r.check(t, automation.CheckOpts{}); !strings.HasPrefix(got, round.CapReached) {
		t.Fatalf("at the cap: %q", got)
	}
}

func TestARefuseWhoseWhyRendersEmptyStillRefuses(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	m := strings.Replace(bundledIntentAs(t, "nudge", "nudge")["intents/nudge/intent.toml"], `when = 'pr.nudge.refusal != ""'`, `when = 'pr.nudge.refusal == ""'`, 1)
	userProfile(t, r.sc.te, map[string]string{"intents/nudge/intent.toml": m})
	if got := r.check(t, automation.CheckOpts{}); got != "its intent refuses it here" {
		t.Fatalf("refusal %q", got)
	}
}

// headlessCalls records the headless turns asked for.
type headlessCalls struct{ got []headless.Request }

func (h *headlessCalls) Start(_ context.Context, r headless.Request) (int64, error) {
	h.got = append(h.got, r)
	return 1, nil
}

func TestATurnStartOnAPullRequestHonorsItsRunner(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Act), "")
	r.st.Args["runner"] = string(dturn.RunnerHeadless)
	h := &headlessCalls{}
	r.steps.d.Headless = h
	got, err := r.run(t, automation.RunOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.got) != 1 || h.got[0].Intent != dturn.Intent(dintent.Act) || h.got[0].Unit != sc.ref.Unit() || h.got[0].Trigger != dturn.Automated {
		t.Fatalf("%q: headless calls %+v", got, h.got)
	}
}
