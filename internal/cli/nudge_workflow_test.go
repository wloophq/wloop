package cli

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const nudgeSilent = "nudge-silent-30d"

// bundledWorkflow is the default profile's workflow name with the config's
// overrides.
func bundledWorkflow(t *testing.T, te *testEnv, name string) workflow.Workflow {
	t.Helper()
	for _, w := range applyConfig(te.loadedIntents().Workflows, te.cfg) {
		if w.Name == name {
			return w
		}
	}
	t.Fatalf("no bundled workflow %s", name)
	return workflow.Workflow{}
}

// silentRig runs the bundled nudge workflow over one pull request's facts
// with steps that record what they would do.
type silentRig struct {
	te    *testEnv
	units *automationtest.Units
	steps *automationtest.Steps
	clock *automationtest.Clock
	eng   *automation.Engine
	u     unit.Unit
}

func newSilentRig(t *testing.T) *silentRig {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	w := bundledWorkflow(t, te, nudgeSilent)
	r := &silentRig{te: te, units: automationtest.NewUnits(), steps: automationtest.NewSteps(map[string]workflow.Reach{builtin.StepTurnStart: workflow.ReachVisible}),
		clock: automationtest.NewClock(testNow), u: unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}}
	r.units.Put(r.u, silentFacts(31, testNow.AddDate(0, 0, -31)))
	r.eng = &automation.Engine{Workflows: func() []workflow.Workflow { return []workflow.Workflow{w} }, Units: r.units, Steps: r.steps,
		Ledger: automationtest.NewLedger(), Clock: r.clock, Config: func() workflow.Facts { return configFacts(te.cfg) }}
	return r
}

func silentFacts(days int, last time.Time) workflow.Facts {
	return workflow.Facts{"pr.repo": "acme/api", "pr.state": "open", "pr.draft": false, "pr.waits_on_user": false,
		"pr.silent_days": days, "pr.last_interaction_at": last, "pr.fingerprint": "fp", "pr.mine": false, "pr.adopted": false}
}

func (r *silentRig) items(t *testing.T) []workflow.Item {
	t.Helper()
	if _, _, err := r.eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	items, err := r.eng.Ledger.Items(context.Background(), automation.Filter{Workflow: nudgeSilent})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestNudgeSilentMatchesAnOpenSilentPRWaitingOnOthers(t *testing.T) {
	t.Parallel()
	r := newSilentRig(t)
	items := r.items(t)
	if len(items) != 1 || items[0].Decision != workflow.DecisionPending || items[0].Default != workflow.OutcomeWait {
		t.Fatalf("items %+v", items)
	}
	if args := items[0].Steps[0].Args; items[0].Steps[0].Use != builtin.StepTurnStart || args["intent"] != string(dintent.Nudge) {
		t.Fatalf("step %+v", items[0].Steps[0])
	}
	if len(r.steps.Ran()) != 0 {
		t.Fatalf("a nudge went out without you: %+v", r.steps.Ran())
	}
}

func TestNudgeSilentSkipsDraftsClosedRecentWaitingOnTheUserOrDevelopedByTheUser(t *testing.T) {
	t.Parallel()
	for name, set := range map[string][2]any{
		"draft":               {"pr.draft", true},
		"closed":              {"pr.state", "closed"},
		"merged":              {"pr.state", "merged"},
		"recent":              {"pr.silent_days", 29},
		"waiting on the user": {"pr.waits_on_user", true},
		"mine":                {"pr.mine", true},
		"taken over":          {"pr.adopted", true},
	} {
		r := newSilentRig(t)
		r.units.Set(r.u, set[0].(string), set[1])
		if items := r.items(t); len(items) != 0 {
			t.Errorf("%s: items %+v", name, items)
		}
	}
}

func TestNudgeSilentDoesNotRepeatWithinASilencePeriod(t *testing.T) {
	t.Parallel()
	r := newSilentRig(t)
	items := r.items(t)
	if _, err := r.eng.Act(context.Background(), items[0].ID, workflow.ActionApply, ""); err != nil {
		t.Fatal(err)
	}
	for day := 1; day <= 3; day++ {
		r.clock.Advance(24 * time.Hour)
		r.units.Set(r.u, "pr.silent_days", 31+day)
		if items := r.items(t); len(items) != 0 {
			t.Fatalf("day %d: planned again %+v", day, items)
		}
	}
	if len(r.steps.Ran()) != 1 {
		t.Fatalf("ran %+v", r.steps.Ran())
	}
}

func TestNudgeSilentRepeatsAfterANewInteractionThenSilence(t *testing.T) {
	t.Parallel()
	r := newSilentRig(t)
	if _, err := r.eng.Act(context.Background(), r.items(t)[0].ID, workflow.ActionApply, ""); err != nil {
		t.Fatal(err)
	}
	comment := r.clock.Now().Add(time.Hour)
	r.units.Put(r.u, silentFacts(0, comment))
	if items := r.items(t); len(items) != 0 {
		t.Fatalf("a fresh comment planned %+v", items)
	}
	r.clock.Advance(31 * 24 * time.Hour)
	r.units.Put(r.u, silentFacts(30, comment))
	items := r.items(t)
	if len(items) != 1 || items[0].Decision != workflow.DecisionPending {
		t.Fatalf("a new silence planned %+v", items)
	}
}

// nudgeStartRig is turn.start with intent nudge over acme/api#7's facts.
func nudgeStartRig(t *testing.T) *startRig {
	t.Helper()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Nudge), "")
	r.st = workflow.Step{Use: builtin.StepTurnStart, Args: map[string]any{"intent": string(dintent.Nudge)}}
	r.why = workflow.Why{Workflow: nudgeSilent, Trigger: workflow.TriggerSweep}
	r.units.Set(r.u, "pr.nudge.refusal", "")
	return r
}

func TestNudgeSilentRespectsCapsPauseHoldAndMute(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	sc := r.sc
	st := r.load(t)
	guards := []struct {
		want     string
		set, off func()
	}{
		{"held by you", func() { r.units.Set(r.u, "pr.held", true) }, func() { r.units.Set(r.u, "pr.held", false) }},
		{whyPaused, func() { sc.te.setPause(t, round.Pause{On: true, At: testNow}) }, func() { sc.te.setPause(t, round.Pause{At: testNow}) }},
		{"muted until", func() {
			r.rec.overrides = map[github.Ref]dinv.Override{sc.ref: {PR: sc.ref, MutedUntil: testNow.Add(time.Hour)}}
		}, func() { r.rec.overrides = nil }},
		{"per-PR daily cap reached (1)", func() {
			sc.te.cfg.Turns.PerUnitPerDay = 1
			st.RecordFollowup(sc.ref.String(), round.Reviewed, "", testNow)
		}, func() { sc.te.cfg.Turns = Defaults().Turns }},
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

func TestNudgeSilentHonorsTheNudgeRefusal(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.units.Set(r.u, "pr.nudge.refusal", "it waits on you")
	if got := r.check(t, automation.CheckOpts{Observe: false}); got != "it waits on you" {
		t.Fatalf("refusal %q", got)
	}
}

func TestBundledWorkflowsLoad(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	want := map[string]struct {
		order         int
		intent, round string
		when          string
		mode          workflow.ApprovalMode
		window        time.Duration
	}{
		"review-follow-up":   {10, string(dintent.Review), roundFollowup, "pr.review.action", workflow.ApprovalDelay, time.Minute},
		"review-first-round": {20, string(dintent.Review), roundFirst, "pr.first.due", workflow.ApprovalDelay, time.Minute},
		"address":            {30, string(dintent.Address), "", "pr.address.due", workflow.ApprovalDelay, time.Minute},
		nudgeSilent:          {40, string(dintent.Nudge), "", "pr.silent_days", workflow.ApprovalWait, 0},
	}
	if broken := te.loadedIntents().BrokenWorkflows; len(broken) > 0 {
		t.Fatalf("broken %v", broken)
	}
	for name, w := range want {
		got := bundledWorkflow(t, te, name)
		args := got.Steps[0].Args
		if got.Provider != unit.GitHub || got.Trigger.Kind != workflow.TriggerSweep || got.Order != w.order || got.Observe ||
			len(got.Steps) != 1 || got.Steps[0].Use != builtin.StepTurnStart || args["intent"] != w.intent || (w.round != "" && args["stage"] != w.round) {
			t.Errorf("%s: %+v", name, got)
		}
		if !slices.Contains(got.When.Reads(), w.when) {
			t.Errorf("%s reads %v", name, got.When.Reads())
		}
		if got.Approval.Mode != w.mode || got.Approval.Window != w.window {
			t.Errorf("%s approval %+v", name, got.Approval)
		}
	}
	if k := bundledWorkflow(t, te, nudgeSilent).Key; !slices.Equal(k, []string{"pr.last_interaction_at"}) {
		t.Errorf("nudge key %v", k)
	}
	if k := bundledWorkflow(t, te, "address").Key; !slices.Equal(k, []string{"pr.fingerprint"}) {
		t.Errorf("address key %v", k)
	}
}
