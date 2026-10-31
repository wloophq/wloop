package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// stubPlanner is a source plugin whose planner plans fixed rounds by unit
// key, keeps the round state of each plan and the effects it applied.
type stubPlanner struct {
	*sourcetest.Stub
	// unsupported answers every effect with src.ErrUnsupported.
	unsupported bool
	// fail answers every effect with it, when set.
	fail    error
	mu      sync.Mutex
	rounds  map[string][]src.Round
	states  []src.RoundState
	effects []src.EffectRequest
}

func newStubPlanner(rounds map[string][]src.Round) *stubPlanner {
	return &stubPlanner{Stub: sourcetest.NewStub(), rounds: rounds}
}

func (p *stubPlanner) Plan(_ context.Context, u plugin.Unit, st src.RoundState) (src.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.states = append(p.states, st)
	return src.Plan{Rounds: p.rounds[u.Key]}, nil
}

func (p *stubPlanner) Payload(context.Context, src.PayloadRequest) (src.PayloadResult, error) {
	return src.PayloadResult{}, nil
}

func (p *stubPlanner) ApplyEffect(_ context.Context, req src.EffectRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.effects = append(p.effects, req)
	if p.unsupported {
		return fmt.Errorf("%w: effect %s", src.ErrUnsupported, req.Effect)
	}
	return p.fail
}

func (p *stubPlanner) Proposals(context.Context, time.Time) (src.Proposed, error) {
	return src.Proposed{}, nil
}

var _ src.Planner = (*stubPlanner)(nil)

func (p *stubPlanner) Placement(context.Context, plugin.Unit, string) (src.Placements, error) {
	return src.Placements{}, nil
}

func (p *stubPlanner) planned() []src.RoundState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]src.RoundState(nil), p.states...)
}

func (p *stubPlanner) applied() []src.EffectRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]src.EffectRequest(nil), p.effects...)
}

// launchPlanner serves p as the github plugin over a real plugin connection
// and makes it the planner of r's steps.
func (r *startRig) launchPlanner(t *testing.T, p *stubPlanner) *src.Client {
	t.Helper()
	c := sourcetest.Launch(t, string(unit.GitHub), p, src.Host{})
	r.steps.d.Planners = map[unit.ProviderID]roundPlanner{unit.GitHub: c}
	return c
}

func TestARoundLaunchesFromAPluginPlan(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	key := sc.ref.String()
	p := newStubPlanner(map[string][]src.Round{key: {{Name: roundFollowup, Workspace: src.WorkspaceRef{Manager: "orca", ID: "ws-rev"},
		Head: "c3", Reason: "the plugin planned it", OnSent: map[string]string{ghsource.MarkReviewed: "fp-plan", "plugin.own": "x"}}}})
	r.launchPlanner(t, p)
	got, err := r.run(t, automation.RunOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "sent the follow-up: the author answered" {
		t.Fatalf("run = %q", got)
	}
	if len(sc.ws.Prompts) != 1 {
		t.Fatalf("prompts %+v", sc.ws.Prompts)
	}
	st := sc.te.loadRounds(t)
	if st.Mark(key, ghsource.MarkReviewed) != "fp-plan" || st.Mark(key, "plugin.own") != "x" || st.Days[round.DayKey(testNow)].PerPR[key] != 1 {
		t.Fatalf("marks %v, rounds today %d", st.PRs[key].Marks, st.Days[round.DayKey(testNow)].PerPR[key])
	}
	if len(p.planned()) == 0 {
		t.Fatal("the plugin was never asked its plan")
	}

	p.mu.Lock()
	p.rounds = nil
	p.mu.Unlock()
	if refusal := r.check(t, automation.CheckOpts{}); refusal != "its follow-up is not due" {
		t.Fatalf("a round the plan drops: %q", refusal)
	}
}

func TestPlanReceivesTheRoundState(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	key := sc.ref.String()
	p := newStubPlanner(map[string][]src.Round{key: {{Name: roundFollowup, Reason: "planned",
		OnSkipped: map[string]string{ghsource.MarkReviewed: "fp-skip", "plugin.gone": ""}}}})
	r.launchPlanner(t, p)
	sent := testNow.Add(-time.Hour)
	st := r.load(t)
	st.RecordSent(key, ghsource.MarkReviewed, "fp-old", sent)
	st.SetMark(key, "plugin.gone", "y")
	st.SetAgentHome(key, "/homes/ana")
	st.Count("acme/api#99", testNow)
	r.save(t)
	blockedAt := testNow.Add(-2 * time.Hour)
	r.rec.events = append(r.rec.events, trustBlockEvent(sc.ref, blockedAt))
	if _, err := sc.te.db.SetOverride(context.Background(), sc.ref.Unit(), map[string]string{port.OverrideNextGuidance: "review"}, nil); err != nil {
		t.Fatal(err)
	}
	r.steps.d.Overrides = sc.te.db

	r.check(t, automation.CheckOpts{})
	states := p.planned()
	if len(states) == 0 {
		t.Fatal("the plugin was never asked its plan")
	}
	got := states[len(states)-1]
	if got.Marks[ghsource.MarkReviewed] != "fp-old" || got.Marks["plugin.gone"] != "y" || !got.SentAt.Equal(sent) || got.AgentHome != "/homes/ana" {
		t.Errorf("bookkeeping %+v", got)
	}
	if got.Today != 1 || got.DayTotal != 2 {
		t.Errorf("today %d of %d, want 1 of 2", got.Today, got.DayTotal)
	}
	if got.Blocked == nil || got.Blocked.Root != "/src/api" || got.Blocked.Folder != "/wt/rev" || !got.Blocked.Since.Equal(blockedAt) {
		t.Errorf("blocked %+v", got.Blocked)
	}
	if got.Overrides[port.OverrideNextGuidance] != "review" || got.Paused {
		t.Errorf("overrides %v, paused %v", got.Overrides, got.Paused)
	}

	r.rec.events = nil
	r.load(t)
	if err := r.steps.Dismiss(context.Background(), r.st, r.u, r.why); err != nil {
		t.Fatal(err)
	}
	r.save(t)
	saved := sc.te.loadRounds(t)
	if saved.Mark(key, ghsource.MarkReviewed) != "fp-skip" || saved.Mark(key, "plugin.gone") != "" {
		t.Fatalf("a skip writes the plan's OnSkipped: %v", saved.PRs[key].Marks)
	}
	if !slicesContainsKind(r.rec.events, dinv.KindRoundSkipped) {
		t.Fatalf("events %+v", r.rec.events)
	}
}

func slicesContainsKind(evs []dinv.Event, kind string) bool {
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestAPluginEffectRunsThroughApplyEffect(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	sc.te.gh.basics = openBasics
	p := newStubPlanner(nil)
	c := sourcetest.Launch(t, string(unit.GitHub), p, src.Host{})
	sc.env.pluginEffects = func(id unit.ProviderID) (effectApplier, bool) { return c, id == unit.GitHub }
	if err := (cliLauncher{sc.env}).Launch(context.Background(), turns.Launch{Unit: sc.ref.Unit(), Intent: dturn.Intent(dintent.Review), Trigger: dturn.ByHand}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, req := range p.applied() {
		names = append(names, req.Effect)
		if req.Unit.Key != sc.ref.String() || req.Turn == nil || req.Turn.ID == "" || req.Draft != nil || req.Route != src.RouteLaunch || req.Inputs != nil {
			t.Errorf("request %+v", req)
		}
	}
	if want := []string{"pr.reviewed"}; !slices.Equal(names, want) {
		t.Fatalf("applied %v, want the review's plugin effects %v", names, want)
	}

	lookup := effectLookup(effectDeps{Appliers: sc.env.draftAppliers()})
	fx, ok := lookup(unit.GitHub, "pr.nudged")
	if !ok {
		t.Fatal("the plugin's draft effect has no applier")
	}
	if err := fx(context.Background(), draft.Draft{ID: 4, Unit: sc.ref.Unit(), Kind: draft.Nudge}); err != nil {
		t.Fatal(err)
	}
	last := p.applied()[len(p.applied())-1]
	if last.Effect != "pr.nudged" || last.Draft == nil || last.Draft.ID != 4 || last.Turn != nil {
		t.Fatalf("draft effect request %+v", last)
	}
}

func TestAnEffectThePluginDoesNotApplyIsAHealthProblemOfASentTurn(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	sc.te.gh.basics = openBasics
	p := newStubPlanner(nil)
	p.unsupported = true
	c := sourcetest.Launch(t, string(unit.GitHub), p, src.Host{})
	sc.env.pluginEffects = func(id unit.ProviderID) (effectApplier, bool) { return c, id == unit.GitHub }
	launch := turns.Launch{Unit: sc.ref.Unit(), Intent: dturn.Intent(dintent.Review), Trigger: dturn.ByHand}
	if err := (cliLauncher{sc.env}).Launch(context.Background(), launch); err != nil {
		t.Fatalf("a turn that went out failed its launch: %v", err)
	}
	if len(p.applied()) != 1 || len(sc.ws.Prompts) != 1 {
		t.Fatalf("applied %+v, prompts %d", p.applied(), len(sc.ws.Prompts))
	}
	if got := sc.env.effectProblems.list(); len(got) != 1 || !strings.Contains(got[0], "pr.reviewed") {
		t.Fatalf("health problems %q, want the unapplied pr.reviewed", got)
	}
	p.mu.Lock()
	p.unsupported = false
	p.mu.Unlock()
	if err := (cliLauncher{sc.env}).Launch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	if got := sc.env.effectProblems.list(); len(got) != 0 {
		t.Fatalf("a turn whose effect applied keeps the problem %q", got)
	}
}

func TestAnEffectThatFailsAfterTheSendIsAHealthProblem(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	sc.te.gh.basics = openBasics
	p := newStubPlanner(nil)
	p.fail = errors.New("the store is locked")
	c := sourcetest.Launch(t, string(unit.GitHub), p, src.Host{})
	sc.env.pluginEffects = func(id unit.ProviderID) (effectApplier, bool) { return c, id == unit.GitHub }
	launch := turns.Launch{Unit: sc.ref.Unit(), Intent: dturn.Intent(dintent.Review), Trigger: dturn.ByHand}
	if err := (cliLauncher{sc.env}).Launch(context.Background(), launch); err != nil {
		t.Fatalf("a turn that went out failed its launch: %v", err)
	}
	if got := sc.env.effectProblems.list(); len(got) != 1 || !strings.Contains(got[0], "pr.reviewed") || !strings.Contains(got[0], "the store is locked") {
		t.Fatalf("health problems %q, want the failed pr.reviewed", got)
	}
}

func TestATurnWhoseEffectsNoPluginAppliesIsNotSent(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	sc.te.gh.basics = openBasics
	sc.env.pluginEffects = func(unit.ProviderID) (effectApplier, bool) { return nil, false }
	err := (cliLauncher{sc.env}).Launch(context.Background(), turns.Launch{Unit: sc.ref.Unit(), Intent: dturn.Intent(dintent.Review), Trigger: dturn.ByHand})
	if err == nil || !strings.Contains(err.Error(), "pr.reviewed") || len(sc.ws.Prompts) != 0 {
		t.Fatalf("launch = %v, prompts %d; want refused before the send", err, len(sc.ws.Prompts))
	}
}

func TestUnblockingWritesTheBlockedRoundsSkipMarks(t *testing.T) {
	t.Parallel()
	sc := addressScenario(t)
	sc.ws.Sessions = nil
	sc.ws.Blocked = workspace.ReasonTrust
	home, _ := untrustedHome(t)
	sc.te.cfg.AgentHome = home
	r := newStartRig(t, sc, string(dintent.Address), "")
	key := sc.ref.String()
	r.planner().edit(key, roundAddress, func(r *src.Round) {
		r.OnSent, r.OnSkipped = map[string]string{"plugin.sent": "s"}, map[string]string{"plugin.x": "1"}
	})
	_, err := r.run(t, automation.RunOpts{})
	var refused automation.Refused
	if !asRefused(err, &refused) || !strings.HasPrefix(refused.Reason, "round blocked: ") {
		t.Fatalf("run = %v", err)
	}
	if err := (envActions{e: sc.env}).Unblock(context.Background(), r.u, daemon.Call{Out: &strings.Builder{}}); err != nil {
		t.Fatal(err)
	}
	marks := sc.te.loadRounds(t).PRs[key].Marks
	if !maps.Equal(marks, map[string]string{"plugin.x": "1"}) {
		t.Fatalf("marks %v, want the plan's skip marks alone", marks)
	}
}

func TestTheStoredRoundStateServesItsMarksAndBlock(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	rec := &memRecorder{}
	sc.env.Rec = rec
	ctx := context.Background()
	key := sc.ref.String()
	st, err := sc.te.Rounds.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.SetMark(key, "plugin.x", "1")
	if err := sc.te.Rounds.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	at := testNow.Add(-time.Hour)
	rec.events = append(rec.events, trustBlockEvent(sc.ref, at))
	got, err := sc.env.storedRoundState(ctx, sc.ref.Unit())
	if err != nil {
		t.Fatal(err)
	}
	if got.Marks["plugin.x"] != "1" || got.Blocked == nil || got.Blocked.Folder != "/wt/rev" || !got.Blocked.Since.Equal(at) {
		t.Fatalf("stored round state %+v, blocked %+v", got, got.Blocked)
	}
}

// firstPlanned is the first round the live planner gh plans on sc's pull
// request over its stored round state; false when it plans none.
func firstPlanned(t *testing.T, sc sweepCase, gh roundPlanner) (src.Round, bool) {
	t.Helper()
	ctx := context.Background()
	stored, err := sc.te.Rounds.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sc.env.roundStateOf(ctx, sc.ref.Unit(), stored, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := gh.Plan(ctx, psource.Wire(sc.ref.Unit()), st)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(plan.Rounds, func(r src.Round) bool { return r.Name == roundFirst })
	if i < 0 {
		return src.Round{}, false
	}
	return plan.Rounds[i], true
}

func TestADismissedFirstRoundIsNotPlannedAgain(t *testing.T) {
	t.Parallel()
	sc := firstRoundCase(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFirst)
	gh := livePlanner(t, sc)
	r.steps.d.Planners = map[unit.ProviderID]roundPlanner{unit.GitHub: gh}
	if first, ok := firstPlanned(t, sc, gh); !ok || first.NotDue != "" {
		t.Fatalf("the first round before the dismissal: %+v %v", first, ok)
	}
	r.load(t)
	if err := r.steps.Dismiss(context.Background(), r.st, r.u, r.why); err != nil {
		t.Fatal(err)
	}
	r.save(t)
	if got := sc.te.loadRounds(t).Mark(sc.ref.String(), ghsource.MarkFirstSkipped); got == "" {
		t.Fatal("the dismissal wrote no skip")
	}
	if first, ok := firstPlanned(t, sc, gh); ok {
		t.Fatalf("a dismissed first round is planned again: %+v", first)
	}
}

func TestAFirstRoundDismissedWhileNotDueStaysSkipped(t *testing.T) {
	t.Parallel()
	sc := firstRoundCase(t)
	dev := wsdomain.Workspace{ID: "ws-dev", RepoID: "r1", Path: "/wt/dev", DisplayName: "feat/new", Status: "todo", HostID: "local", Branch: "feat/new", LinkedPR: 20}
	sc.ws.WorkspaceList = append(sc.ws.WorkspaceList, dev)
	r := newStartRig(t, sc, string(dintent.Review), roundFirst)
	gh := livePlanner(t, sc)
	r.steps.d.Planners = map[unit.ProviderID]roundPlanner{unit.GitHub: gh}
	first, ok := firstPlanned(t, sc, gh)
	if !ok || first.NotDue == "" {
		t.Fatalf("the first round while its author workspace exists: %+v %v", first, ok)
	}
	if refusal := r.check(t, automation.CheckOpts{}); refusal != "its first round is not due" {
		t.Fatalf("refusal %q", refusal)
	}
	r.load(t)
	if err := r.steps.Dismiss(context.Background(), r.st, r.u, r.why); err != nil {
		t.Fatal(err)
	}
	r.save(t)
	sc.ws.WorkspaceList = slices.DeleteFunc(sc.ws.WorkspaceList, func(w wsdomain.Workspace) bool { return w.ID == dev.ID })
	if first, ok := firstPlanned(t, sc, gh); ok {
		t.Fatalf("a first round dismissed while not due is planned once due: %+v", first)
	}
}
