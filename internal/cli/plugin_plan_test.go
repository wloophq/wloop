package cli

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// stubPlanner is a source plugin whose planner plans fixed rounds by unit
// key, keeps the round state of each plan and the effects it applied.
type stubPlanner struct {
	*sourcetest.Stub
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
	return nil
}

func (p *stubPlanner) Proposals(context.Context, time.Time) ([]src.Draft, error) { return nil, nil }

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
		Head: "c3", Reason: "the plugin planned it", OnSent: map[string]string{round.Reviewed: "fp-plan", "plugin.own": "x"}}}})
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
	if st.Mark(key, round.Reviewed) != "fp-plan" || st.Mark(key, "plugin.own") != "x" || st.Days[round.DayKey(testNow)].PerPR[key] != 1 {
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
		OnSkipped: map[string]string{round.Reviewed: "fp-skip", "plugin.gone": ""}}}})
	r.launchPlanner(t, p)
	sent := testNow.Add(-time.Hour)
	st := r.load(t)
	st.RecordSent(key, round.Reviewed, "fp-old", sent)
	st.SetMark(key, "plugin.gone", "y")
	st.SetAgentHome(key, "/homes/ana")
	st.Count("acme/api#99", testNow)
	r.save(t)
	blockedAt := testNow.Add(-2 * time.Hour)
	r.rec.events = append(r.rec.events, trustBlockEvent(sc.ref, blockedAt))
	if _, err := sc.te.db.SetOverride(context.Background(), sc.ref.Unit(), map[string]string{port.OverrideMutedKind: "review"}, nil); err != nil {
		t.Fatal(err)
	}
	r.steps.d.Overrides = sc.te.db

	r.check(t, automation.CheckOpts{})
	states := p.planned()
	if len(states) == 0 {
		t.Fatal("the plugin was never asked its plan")
	}
	got := states[len(states)-1]
	if got.Marks[round.Reviewed] != "fp-old" || got.Marks["plugin.gone"] != "y" || !got.SentAt.Equal(sent) || got.AgentHome != "/homes/ana" {
		t.Errorf("bookkeeping %+v", got)
	}
	if got.Today != 1 || got.DayTotal != 2 {
		t.Errorf("today %d of %d, want 1 of 2", got.Today, got.DayTotal)
	}
	if got.Blocked == nil || got.Blocked.Root != "/src/api" || got.Blocked.Folder != "/wt/rev" || !got.Blocked.Since.Equal(blockedAt) {
		t.Errorf("blocked %+v", got.Blocked)
	}
	if got.Overrides[port.OverrideMutedKind] != "review" || got.Paused {
		t.Errorf("overrides %v, paused %v", got.Overrides, got.Paused)
	}

	r.rec.events = nil
	r.load(t)
	if err := r.steps.Dismiss(context.Background(), r.st, r.u, r.why); err != nil {
		t.Fatal(err)
	}
	r.save(t)
	saved := sc.te.loadRounds(t)
	if saved.Mark(key, round.Reviewed) != "fp-skip" || saved.Mark(key, "plugin.gone") != "" {
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
		if req.Unit.Key != sc.ref.String() || req.Turn == nil || req.Turn.ID == "" || req.Draft != nil {
			t.Errorf("request %+v", req)
		}
	}
	if want := []string{"pr.reviewed", "pr.round_sent"}; !slices.Equal(names, want) {
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
