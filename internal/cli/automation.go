package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	coresweep "github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/domain/gtd"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/source"
)

var _ workspaces.FetchLog = (*sqlite.Fetches)(nil)

// engineDeps are what the engine runs over besides the Env. SessionFacts,
// WorkspaceFacts and Candidates override the Env's session and workspace
// providers and the github module's rounds.
type engineDeps struct {
	DB             *sqlite.DB
	Modules        []source.Module
	SessionFacts   automation.Units
	WorkspaceFacts automation.Units
	// Snapshot overrides the Env's workspace snapshot for the removal's
	// session guard.
	Snapshot   coresweep.Snapshot
	Candidates roundCandidates
	Briefs     *brief.Queue
	// Asker answers classify's headless calls; nil refuses every classify.
	Asker turns.Asker
	// Pub hears the engine's events once stored and the steps' once
	// recorded; with a DB, the steps' are recorded on their units.
	Pub port.Publisher
	Log func(format string, args ...any)
	// Subjects are facts providers of the core-kept subjects beyond the
	// session and workspace ones, such as entry, by provider.
	Subjects map[unit.ProviderID]automation.Units
	// Withdrawals are the drafts the workflows proposed; nil withdraws none.
	Withdrawals automation.Withdrawals
	// TurnStart are what turn.start reads on units outside the round
	// providers.
	TurnStart turnStartDeps
	// Drafts are what draft.propose reads; nil refuses it.
	Drafts *draftDeps
}

// newEngine assembles the engine over the store, the facts providers and
// the builtin steps, and keeps it as the Env's: the sweep's.
func (e *Env) newEngine(d engineDeps) *automation.Engine {
	eng := e.assemble(d)
	e.engine = eng
	return eng
}

// planEngine is an engine for planning only, the API's: its own session and
// workspace providers, so its reads never move the sweep's Seen.
func (e *Env) planEngine(d engineDeps) *automation.Engine { return e.assemble(d) }

func (e *Env) assemble(d engineDeps) *automation.Engine {
	if d.SessionFacts == nil {
		d.SessionFacts = e.sessionFacts()
	}
	if d.WorkspaceFacts == nil {
		d.WorkspaceFacts = e.workspaceFacts()
	}
	providers := defaultFacts(d.SessionFacts, d.WorkspaceFacts)
	for id, u := range d.Subjects {
		providers[id] = u
	}
	codecs := []unit.Codec{pr.Codec{}, gtd.Codec{}}
	for _, m := range d.Modules {
		if m.Facts != nil {
			providers[m.ID] = m.Facts
		}
		if m.Codec != nil && !slices.ContainsFunc(codecs, func(c unit.Codec) bool { return c.Provider() == m.ID }) {
			codecs = append(codecs, m.Codec)
		}
	}
	codecs = append(codecs, sessionAndWorkspaceCodecs()...)
	units := newUnitSet(providers)
	if d.Candidates == nil {
		d.Candidates = candidatesOf(d.Modules)
	}
	sd := stepDeps{Units: units, Candidates: d.Candidates, Rounds: e.Rounds, Drafts: d.Drafts, Pub: d.Pub, Log: d.Log, Headless: d.TurnStart.Headless, Turns: d.TurnStart.Turns}
	if d.DB != nil {
		sd.Pub = stepEvents{rec: sqlite.New(d.DB), next: d.Pub, log: d.Log}
	}
	if d.Briefs != nil {
		sd.Briefs = brief.Intakes{Q: d.Briefs}
		sd.Classify.BreakerOpen = func() bool { open, _, _, _ := d.Briefs.Breaker(); return open }
	}
	sd.Removal.Snapshot = d.Snapshot
	if sd.Removal.Snapshot == nil {
		sd.Removal.Snapshot = e.planSnapshot()
	}
	if d.DB != nil {
		sd.Classify.Store = d.DB.Classifications()
		sd.Removal.Fetched = d.DB.Fetches()
	}
	sd.Classify.Asker = d.Asker
	if e.changes != nil {
		sd.Changes = e.changes
	}
	steps := newStepSet(e, sd)
	workflows := func() []workflow.Workflow {
		cfg, _ := e.loadConfig()
		l := e.loadedIntents()
		return applyConfigWith(l.Workflows, cfg, briefExtrasOf(l.Registry))
	}
	units.classified = &classifiedUnits{c: steps.classify, workflows: workflows}
	if d.DB != nil {
		units.briefed = &briefedFacts{store: inventory.New(d.DB), intent: e.briefIntents(func(string, ...any) {})}
	}
	eng := &automation.Engine{
		Workflows: workflows,
		Broken:    e.brokenWorkflows,
		Units:     units,
		Steps:     steps,
		Guidance:  recGuidance{e},
		Clock:     clockFunc(func() time.Time { return e.Now() }),
		Codecs:    unit.NewRegistry(codecs...),
		Pause:     e.automationPause,
		Config: func() workflow.Facts {
			cfg, _ := e.loadConfig()
			return configFacts(cfg)
		},
		State: steps.states(),
		Pub:   d.Pub,
		Log:   d.Log,
	}
	if d.DB != nil {
		eng.Ledger, eng.Events, eng.Marks = d.DB.Ledger(), d.DB.Events(), d.DB
	}
	if d.Withdrawals != nil {
		eng.Withdrawals = d.Withdrawals
		eng.PluginEnabled = func(p unit.ProviderID) bool { return e.currentConfig().PluginEnabled(string(p)) }
	}
	if e.WS.Len() > 0 {
		eng.Reachable = e.WS.Reachable
	}
	if e.changes != nil {
		eng.Changes = e.changes
	}
	return eng
}

// currentConfig is the config the daemon holds, the file's outside it.
// brokenWorkflows are the user workflows that failed to load.
func (e *Env) brokenWorkflows() []string {
	return slices.Collect(maps.Keys(e.loadedIntents().BrokenWorkflows))
}

func (e *Env) currentConfig() config.Config {
	cfg, _ := e.loadConfig()
	return cfg
}

func (e *Env) automationPause(ctx context.Context) (automation.Pause, error) {
	if e.Rounds == nil {
		return automation.Pause{}, nil
	}
	p, err := e.Rounds.Pause(ctx)
	if err != nil {
		return automation.Pause{}, err
	}
	return automation.Pause{On: p.On, Repos: p.Repos}, nil
}

// applyConfig is the loaded workflows with the config's overrides: those
// disabled dropped, the mode and approval set.
func applyConfig(ws []workflow.Workflow, cfg config.Config) []workflow.Workflow {
	return applyConfigWith(ws, cfg, nil)
}

// applyConfigWith is applyConfig whose only_when also reads the brief.extra.*
// facts extras gives for a workflow's provider; nil extras gives none.
func applyConfigWith(ws []workflow.Workflow, cfg config.Config, extras func(unit.ProviderID) expr.Env) []workflow.Workflow {
	out := make([]workflow.Workflow, 0, len(ws))
	for _, w := range ws {
		wc := cfg.Workflows[w.Name]
		if wc.Enabled != nil && !*wc.Enabled {
			continue
		}
		if wc.Observe != nil {
			w.Observe = *wc.Observe
		}
		if o, ok := runOverlay(wc.Approval); ok {
			a, err := intentload.OverlayRunApproval(w.Approval, o, workflowEnv(w, extras))
			if err != nil {
				w.When = workflow.Refused{Err: fmt.Errorf("workflows.%s.approval: %w", w.Name, err)}
			}
			w.Approval = a
		}
		if wc.OnlyWhen != "" {
			w.When = workflow.All(w.When, onlyWhen(w, wc.OnlyWhen, extras))
		}
		if p, _ := configPolicy(w, wc.DraftApproval()); p != nil {
			w.DraftApproval = p
		}
		out = append(out, w)
	}
	return out
}

// onlyWhen is the config's only_when of w compiled over w's facts; one that
// does not compile refuses every unit, so the workflow runs nowhere.
func onlyWhen(w workflow.Workflow, src string, extras func(unit.ProviderID) expr.Env) workflow.Predicate {
	p, err := expr.CompileWorkflow(src, workflowEnv(w, extras))
	if err != nil {
		return workflow.Refused{Err: fmt.Errorf("workflows.%s.only_when: %w", w.Name, err)}
	}
	return p
}

// workflowEnv is w's facts, with the brief.extra.* facts extras gives for
// its provider.
func workflowEnv(w workflow.Workflow, extras func(unit.ProviderID) expr.Env) expr.Env {
	env := builtin.WorkflowEnv(w.Slot)
	if extras != nil && slices.Contains(builtin.BriefSubjects, w.Slot) {
		maps.Copy(env, extras(w.Provider))
	}
	return env
}

// runOverlay is the config's approval block as an overlay; false when it
// sets none.
func runOverlay(a *config.ApprovalConfig) (intentload.ApprovalOverlay, bool) {
	if a == nil {
		return intentload.ApprovalOverlay{}, false
	}
	return overlay(*a), true
}

// overlay is a, a config approval block, as an overlay of a manifest's.
func overlay(a config.ApprovalConfig) intentload.ApprovalOverlay {
	o := intentload.ApprovalOverlay{Mode: a.Mode}
	if a.Window != nil {
		d := time.Duration(*a.Window)
		o.Window = &d
	}
	if a.Rule != nil {
		o.Rules = make([]intentload.RuleOverlay, len(a.Rule))
		for i, r := range a.Rule {
			o.Rules[i] = intentload.RuleOverlay{When: r.When, Mode: r.Mode, Window: time.Duration(r.Window)}
		}
	}
	return o
}

// briefExtrasOf are the brief.extra.* facts of each provider's brief intent
// in reg.
func briefExtrasOf(reg *dintent.Registry) func(unit.ProviderID) expr.Env {
	return func(p unit.ProviderID) expr.Env {
		d, _ := reg.Builder(builtin.BuildsBrief, p)
		return builtin.ExtraFacts(d.Extra)
	}
}

// configFacts are the config slot of a workflow's predicates.
func configFacts(cfg config.Config) workflow.Facts {
	c := cfg.Cleanup
	return workflow.Facts{
		"config.cleanup.idle":            time.Duration(c.Idle),
		"config.cleanup.asked_idle":      time.Duration(c.AskedIdle),
		"config.cleanup.workspace_grace": time.Duration(c.WorkspaceGrace),
		"config.cleanup.manual_idle":     time.Duration(c.ManualIdle),
		"config.cleanup.stale_workspace": time.Duration(c.StaleWorkspace),
		"config.nudge.silent_days":       cfg.Nudge.SilentDays,
		"config.gtd.stale_waiting_days":  cfg.GTD.StaleWaitingDays,
		"config.drafts.repeat_after":     time.Duration(cfg.Drafts.RepeatAfter),
		"config.turns.per_day":           cfg.Turns.PerDay,
	}
}

// recGuidance is the guidance the user stored for a pull request's next
// round.
type recGuidance struct{ e *Env }

func (g recGuidance) Peek(ctx context.Context, u unit.Unit) (string, error) {
	ref, err := githubRef(u)
	if err != nil || g.e.Rec == nil {
		return "", nil
	}
	overrides, err := g.e.Rec.Overrides(ctx)
	if err != nil {
		return "", err
	}
	return overrides[ref].NextGuidance, nil
}

func (g recGuidance) Take(ctx context.Context, u unit.Unit) (string, error) {
	ref, err := githubRef(u)
	if err != nil || g.e.Rec == nil {
		return "", nil
	}
	return g.e.Rec.TakeNextGuidance(ctx, ref)
}

func (g recGuidance) Keep(ctx context.Context, u unit.Unit, guidance string) error {
	ref, err := githubRef(u)
	if err != nil || g.e.Rec == nil || guidance == "" {
		return nil
	}
	return g.e.Rec.KeepNextGuidance(context.WithoutCancel(ctx), ref, guidance)
}

// roundState is the sweep's round state, the one the steps write through
// between its Load and its Save.
type roundState struct {
	st     *round.Store
	rounds Rounds
	steps  *stepSet
}

// Rounds is the round store, for the facts providers' Settle.
func (s *roundState) Rounds() *round.Store { return s.st }

func (s *roundState) Save(ctx context.Context) error {
	defer s.steps.unbind(s.st)
	return s.rounds.Save(context.WithoutCancel(ctx), s.st)
}

// roundStates loads the round state once per sweep, by-hand action or due
// run, and binds it to the steps.
type roundStates struct{ steps *stepSet }

func (l roundStates) Load(ctx context.Context) (automation.State, error) {
	if l.steps.d.Rounds == nil {
		return nil, errors.New("no round state: " + errNoStore.Error())
	}
	st, err := l.steps.d.Rounds.Load(ctx)
	if err != nil {
		return nil, err
	}
	l.steps.bind(st)
	return &roundState{st: st, rounds: l.steps.d.Rounds, steps: l.steps}, nil
}

// pendingRounds are the engine's pending turn.start items as the read model
// lists pending automatic rounds.
func (e *Env) pendingRounds(ctx context.Context) (map[unit.Unit]round.Pending, error) {
	if e.engine == nil {
		return nil, nil
	}
	items, err := e.engine.Pending(ctx)
	if err != nil {
		return nil, err
	}
	out := map[unit.Unit]round.Pending{}
	for _, it := range items {
		i := slices.IndexFunc(it.Steps, isTurnStart)
		if i < 0 {
			continue
		}
		out[it.Unit] = round.Pending{Intent: e.startOf(it.Steps[i], it.Unit.Provider).kindName(), Reason: it.Reason, Since: it.At, Deadline: it.Deadline}
	}
	return out, nil
}

// enginePending is pendingRounds as the read model's port.
type enginePending struct{ e *Env }

func (p enginePending) Pending(ctx context.Context) (map[unit.Unit]round.Pending, error) {
	return p.e.pendingRounds(ctx)
}

// meteredEngine runs the user's and the deadline's runs under the gate
// meter scope and meters the sweep's wait for the loop lock.
type meteredEngine struct{ *automation.Engine }

func (m meteredEngine) Sweep(ctx context.Context, loop sync.Locker) (string, string, error) {
	return m.Engine.Sweep(ctx, meteredLock{ctx: ctx, l: loop})
}

func (m meteredEngine) DueRun(ctx context.Context) error {
	ctx, scope := meter.Start(ctx, meter.Loop("gate"))
	defer scope.End()
	return m.Engine.DueRun(ctx)
}

func (m meteredEngine) Act(ctx context.Context, id string, a workflow.Action, guidance string) (workflow.Item, error) {
	ctx, scope := meter.Start(ctx, meter.Loop("gate"))
	defer scope.End()
	return m.Engine.Act(ctx, id, a, guidance)
}

type meteredLock struct {
	ctx context.Context
	l   sync.Locker
}

func (m meteredLock) Lock() {
	_, wait := meter.Start(m.ctx, "lockwait")
	m.l.Lock()
	wait.End()
}

func (m meteredLock) Unlock() { m.l.Unlock() }

// unitSet is every facts provider by its provider id, with the optional
// capabilities of each reaching the engine.
type unitSet struct {
	providers map[unit.ProviderID]automation.Units
	order     []unit.ProviderID
	// classified adds the classify outputs to the facts; nil adds none.
	classified *classifiedUnits
	// briefed adds the brief.* facts; nil adds none.
	briefed *briefedFacts
}

var (
	_ automation.Refresher = (*unitSet)(nil)
	_ automation.Settler   = (*unitSet)(nil)
	_ automation.Gainer    = (*unitSet)(nil)
	_ automation.Owner     = (*unitSet)(nil)
	_ automation.Freshener = (*unitSet)(nil)
	_ automation.Seener    = (*unitSet)(nil)
	_ automation.Dueer     = (*unitSet)(nil)
)

func newUnitSet(providers map[unit.ProviderID]automation.Units) *unitSet {
	s := &unitSet{providers: map[unit.ProviderID]automation.Units{}}
	for id, p := range providers {
		if p != nil {
			s.providers[id] = p
			s.order = append(s.order, id)
		}
	}
	slices.Sort(s.order)
	return s
}

func (s *unitSet) provider(id unit.ProviderID) (automation.Units, error) {
	if p, ok := s.providers[id]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("no facts provider for %s", id)
}

func (s *unitSet) List(ctx context.Context, id unit.ProviderID) ([]unit.Unit, error) {
	p, err := s.provider(id)
	if err != nil {
		return nil, err
	}
	return p.List(ctx, id)
}

func (s *unitSet) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	p, err := s.provider(u.Provider)
	if err != nil {
		return nil, err
	}
	f, err := p.Facts(ctx, u)
	if err == nil && s.briefed != nil {
		f, err = s.briefed.add(ctx, u, f)
	}
	if err != nil || s.classified == nil {
		return f, err
	}
	return s.classified.add(ctx, u, f)
}

func (s *unitSet) Refresh(ctx context.Context) error {
	var errs []error
	for _, id := range s.order {
		if r, ok := s.providers[id].(automation.Refresher); ok {
			if err := r.Refresh(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (s *unitSet) Settle(ctx context.Context, st automation.State) error {
	var errs []error
	for _, id := range s.order {
		if r, ok := s.providers[id].(automation.Settler); ok {
			if err := r.Settle(ctx, st); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (s *unitSet) Gain(ctx context.Context, u unit.Unit) (workflow.Gain, error) {
	if g, ok := s.providers[u.Provider].(automation.Gainer); ok {
		return g.Gain(ctx, u)
	}
	return workflow.Gain{}, nil
}

func (s *unitSet) Owners(ctx context.Context, u unit.Unit) ([]unit.Unit, error) {
	if o, ok := s.providers[u.Provider].(automation.Owner); ok {
		return o.Owners(ctx, u)
	}
	return nil, nil
}

func (s *unitSet) Moved(ctx context.Context, units []unit.Unit) ([]unit.Unit, error) {
	byProvider := map[unit.ProviderID][]unit.Unit{}
	for _, u := range units {
		byProvider[u.Provider] = append(byProvider[u.Provider], u)
	}
	var out []unit.Unit
	for _, id := range s.order {
		f, ok := s.providers[id].(automation.Freshener)
		if !ok || len(byProvider[id]) == 0 {
			continue
		}
		moved, err := f.Moved(ctx, byProvider[id])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, moved...)
	}
	return out, nil
}

// Seen is the workspace snapshot generation the provider that reads it saw.
func (s *unitSet) Seen() port.Generation {
	var g port.Generation
	for _, id := range s.order {
		if r, ok := s.providers[id].(automation.Seener); ok {
			g = max(g, r.Seen())
		}
	}
	return g
}

func (s *unitSet) Due() time.Time {
	var due time.Time
	for _, id := range s.order {
		if d, ok := s.providers[id].(automation.Dueer); ok {
			if t := d.Due(); !t.IsZero() && (due.IsZero() || t.Before(due)) {
				due = t
			}
		}
	}
	return due
}
