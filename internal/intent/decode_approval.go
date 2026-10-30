package intent

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// mApproval is a [gates.approval] table, of a manifest or of gates.toml.
type mApproval struct {
	Mode   *string         `toml:"mode"`
	Window *string         `toml:"window"`
	Rule   []mApprovalRule `toml:"rule"`
}

type mApprovalRule struct {
	When   *string `toml:"when"`
	Mode   *string `toml:"mode"`
	Window *string `toml:"window"`
}

// gateApproval is a manifest's [gates.approval] as loaded: its rules, each
// for the kinds of gate it applies to, and the table's mode, nil when it
// sets none.
type gateApproval struct {
	rules []gateRule
	mode  *gateRule
}

// gateRule is one rule, compiled over a draft's facts for a draft gate
// and over its unit's facts for a gate of any other kind.
type gateRule struct {
	kinds  []gate.Kind
	mode   gate.Mode
	window time.Duration
	draft  draft.Rule
	unit   workflow.Predicate
	// byKind is the when of a rule that reads gate.kind alone, which holds
	// or not for every gate of a kind; nil for any other.
	byKind workflow.Predicate
}

func (r gateRule) appliesTo(k gate.Kind) bool { return slices.Contains(r.kinds, k) }

// holdsFor says whether r, a rule over gate.kind alone, holds for every
// gate of kind k; false for any other rule, which decides gate by gate.
func (r gateRule) holdsFor(k gate.Kind) (static, holds bool) {
	if r.byKind == nil {
		return false, false
	}
	ok, err := r.byKind.Match(workflow.Vars{Facts: withGate(nil, k)})
	return true, err == nil && ok
}

const gateKindFact = builtin.GateSlot + ".kind"

// draftFacts are the facts only a draft gate has.
const draftFacts = builtin.DraftSlot + "."

// gateApproval is the [gates.approval] of a manifest whose gates are of
// kinds: draftEnv types a draft gate's rule, unitEnv a rule of any other
// kind's, nil when there is none to compile. A rule applies to the kinds
// its when pins gate.kind to, else to draft gates alone when it reads a
// draft's facts, else to every kind whose env compiles it. dry_run is a
// draft gate's only: one pinned to another kind fails to load.
func (s *sheet) gateApproval(m *mApproval, kinds []gate.Kind, draftEnv, unitEnv expr.Env) *gateApproval {
	if m == nil {
		return nil
	}
	a := &gateApproval{}
	switch {
	case m.Mode == nil && m.Window != nil:
		s.add("gates.approval.window", "is read only with mode delay, which the table does not set")
	case m.Mode != nil:
		if mode, w, ok := s.modeWindow("gates.approval", m.Mode, m.Window, builtin.GateModes(), "the gate's consequence"); ok {
			a.mode = &gateRule{kinds: kindsOf(kinds, gate.Mode(mode)), mode: gate.Mode(mode), window: w}
			s.dryRunSkips("gates.approval.mode", gate.Mode(mode), kinds)
		}
	}
	for i, r := range m.Rule {
		path := fmt.Sprintf("gates.approval.rule[%d]", i)
		mode, w, ok := s.modeWindow(path, r.Mode, r.Window, builtin.GateModes(), "the gate's consequence")
		if r.When == nil {
			s.add(path+".when", "is required: a predicate over the gate's facts and its unit's")
			continue
		}
		if !ok {
			continue
		}
		if rule, ok := s.gateRule(path+".when", *r.When, gate.Mode(mode), w, kinds, draftEnv, unitEnv); ok {
			a.rules = append(a.rules, rule)
		}
	}
	return a
}

// kindsOf are the kinds of kinds mode applies to.
func kindsOf(kinds []gate.Kind, mode gate.Mode) []gate.Kind {
	var out []gate.Kind
	for _, k := range kinds {
		if mode.AppliesTo(k) {
			out = append(out, k)
		}
	}
	return out
}

func (s *sheet) gateRule(path, when string, mode gate.Mode, window time.Duration, kinds []gate.Kind, draftEnv, unitEnv expr.Env) (gateRule, bool) {
	r, warn, err := compileRule(when, mode, window, kinds, draftEnv, unitEnv)
	var plain plainError
	switch {
	case errors.As(err, &plain):
		s.add(path, "%s", plain.msg)
		return r, false
	case err != nil:
		s.ruleProblem(path, when, err, withGateFacts(draftEnv, unitEnv))
		return r, false
	}
	if warn != "" {
		s.warn(path, "%s", warn)
	}
	return r, len(r.kinds) > 0
}

// plainError is why a rule does not load, said as is.
type plainError struct{ msg string }

func (e plainError) Error() string { return e.msg }

// compileRule is a [gates.approval] rule for the gates of kinds it applies
// to: draftEnv types its when for a draft gate, unitEnv for any other kind,
// nil when there is none to compile. err is why it applies to none, or to
// none of the kinds its when pins; warning is what loads but misleads.
func compileRule(when string, mode gate.Mode, window time.Duration, kinds []gate.Kind, draftEnv, unitEnv expr.Env) (r gateRule, warning string, err error) {
	r = gateRule{mode: mode, window: window}
	probe, err := expr.CompileWorkflow(when, withGateFacts(draftEnv, unitEnv))
	if err != nil {
		return r, "", err
	}
	pinned := pinnedKinds(probe)
	if reads := probe.Reads(); len(reads) > 0 && !slices.ContainsFunc(reads, func(f string) bool { return f != gateKindFact }) {
		r.byKind = probe
	}
	if k := nonDraftPin(probe); mode == gate.ModeDryRun && k != "" {
		return r, "", plainError{fmt.Sprintf("pins gate.kind to %s with mode dry_run, which applies to draft gates only", k)}
	}
	cands := kinds
	switch {
	case len(pinned) > 0:
		cands = nil
		for _, k := range pinned {
			if slices.Contains(kinds, k) {
				cands = append(cands, k)
			}
		}
		if len(cands) == 0 {
			return r, "", plainError{fmt.Sprintf("pins gate.kind to %s, which no gate of this manifest is", pinned[0])}
		}
	case slices.ContainsFunc(probe.Reads(), func(f string) bool { return strings.HasPrefix(f, draftFacts) }):
		cands = slices.DeleteFunc(slices.Clone(kinds), func(k gate.Kind) bool { return k != gate.KindDraft })
	}
	if mode == gate.ModeDryRun && slices.ContainsFunc(cands, func(k gate.Kind) bool { return k != gate.KindDraft }) {
		warning = dryRunSkipped
	}
	cands = kindsOf(cands, mode)
	var draftErr, unitErr error
	if slices.Contains(cands, gate.KindDraft) && draftEnv != nil {
		if r.draft, draftErr = builtin.ApprovalRule(when, draft.Approval{Mode: draft.ModeOf(mode), Window: window}, withGateFacts(draftEnv)); draftErr == nil {
			if builtin.NeverNamesADelivery(r.draft) {
				warning = "reads draft.delivery but names none, so it never releases a draft whose delivery posts; to release one, write " + builtin.DeliveryForm
			}
			r.draft.When = gateCondition{r.draft.When, gate.KindDraft}
			r.kinds = append(r.kinds, gate.KindDraft)
		}
	}
	if slices.ContainsFunc(cands, func(k gate.Kind) bool { return k != gate.KindDraft }) && unitEnv != nil {
		env := withGateFacts(unitEnv)
		if r.unit, unitErr = expr.CompileWorkflow(when, env); unitErr == nil && mode != gate.ModeWait {
			if f := untrustedRead(r.unit, env); f != "" {
				unitErr = &builtin.UntrustedError{Fact: f}
			}
		}
		if unitErr == nil {
			for _, k := range cands {
				if k != gate.KindDraft {
					r.kinds = append(r.kinds, k)
				}
			}
		}
	}
	var untrusted *builtin.UntrustedError
	var subjects *builtin.SubjectsError
	if errors.As(draftErr, &untrusted) || errors.As(draftErr, &subjects) {
		return r, "", fmt.Errorf("%w; %s", draftErr, reachesDrafts)
	}
	if failed := cmpErr(unitErr, draftErr); failed != nil && (len(r.kinds) == 0 || len(pinned) > 0) {
		return r, "", failed
	}
	return r, warning, nil
}

func cmpErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// reachesDrafts is why a rule a draft gate may not read is refused rather
// than left to the other kinds: rules are tried in order, so dropping it
// from draft gates would let a later rule decide the draft in its place.
const reachesDrafts = `it reaches draft gates, where a later rule would decide in its place; pin it to the other kinds, such as gate.kind == "plan"`

// dryRunSkipped is why a dry_run that pins no kind decides only the draft
// gates of a manifest that opens others.
const dryRunSkipped = "is dry_run, which applies to draft gates only: the manifest's other gates take the next rule or their default"

// dryRunSkips warns at path when mode is dry_run and kinds hold a kind
// but draft, which it does not decide.
func (s *sheet) dryRunSkips(path string, mode gate.Mode, kinds []gate.Kind) {
	if mode == gate.ModeDryRun && slices.ContainsFunc(kinds, func(k gate.Kind) bool { return k != gate.KindDraft }) {
		s.warn(path, "%s", dryRunSkipped)
	}
}

// withGateFacts is the union of envs with the gate's facts.
func withGateFacts(envs ...expr.Env) expr.Env {
	out := gateEnv()
	for _, e := range envs {
		maps.Copy(out, e)
	}
	return out
}

// ruleProblem places why a rule's when does not compile; one that reads a
// fact only the loop names where an agent names it says so.
func (s *sheet) ruleProblem(path, when string, err error, env expr.Env) {
	var untrusted *builtin.UntrustedError
	var subjects *builtin.SubjectsError
	if errors.As(err, &untrusted) || errors.As(err, &subjects) {
		s.add(path, "%s", err.Error())
		return
	}
	for _, name := range builtin.AutoOnly {
		if _, ok := env[name]; ok {
			continue
		}
		with := maps.Clone(env)
		with[name] = builtin.ApprovalEnv(true)[name]
		if _, perr := builtin.ApprovalRule(when, draft.Approval{Mode: draft.Wait}, with); perr == nil {
			s.add(path, "reads %s, which an intent's rule may not: its turns' agents name their drafts' purpose and effects", name)
			return
		}
	}
	msg, snippet := exprMsg(err)
	s.add(path, "%s", msg)
	if snippet != "" {
		s.problems[len(s.problems)-1].Snippet = snippet
	}
}

// modeWindow is the mode, one of modes, and the window at path: a window
// only with delay, which needs one.
func (s *sheet) modeWindow(path string, mode, window *string, modes []builtin.Value, what string) (string, time.Duration, bool) {
	if mode == nil {
		s.add(path+".mode", "is required: one of %s", quoted(builtin.ValueNames(modes)))
		return "", 0, false
	}
	delay := *mode == string(gate.ModeDelay)
	switch {
	case !slices.Contains(builtin.ValueNames(modes), *mode):
		s.oneOf(path+".mode", *mode, modes)
		return "", 0, false
	case delay && window == nil:
		s.add(path+".window", "is required with mode delay: how long the user has before %s, such as 10m", what)
		return "", 0, false
	case !delay && window != nil:
		s.add(path+".window", "is read only with mode delay, not with %s", *mode)
		return "", 0, false
	case window == nil:
		return *mode, 0, true
	}
	n := len(s.problems)
	w := s.duration(path+".window", *window)
	return *mode, w, len(s.problems) == n
}

// draftPolicy is a's policy for draft gates: its draft rules, then its
// mode, else gates.toml's default for a draft gate; nil when a sets
// neither a rule nor a mode for one.
func (a *gateApproval) draftPolicy(gates GatesDef) *draft.Policy {
	if a == nil {
		return nil
	}
	p := &draft.Policy{}
	for _, r := range a.rules {
		if !r.appliesTo(gate.KindDraft) {
			continue
		}
		switch static, holds := r.holdsFor(gate.KindDraft); {
		case static && holds:
			p.Default = draft.Approval{Mode: draft.ModeOf(r.mode), Window: r.window}
			return p
		case !static:
			p.Rules = append(p.Rules, r.draft)
		}
	}
	switch {
	case a.mode != nil && a.mode.appliesTo(gate.KindDraft):
		p.Default = draft.Approval{Mode: draft.ModeOf(a.mode.mode), Window: a.mode.window}
	case len(p.Rules) == 0:
		return nil
	default:
		p.Default = DefaultDraftApproval(gates)
	}
	return p
}

// DefaultDraftApproval is gates.toml's default for a draft gate.
func DefaultDraftApproval(gates GatesDef) draft.Approval {
	m, w, _ := gates.DefaultFor(gate.KindDraft)
	return draft.Approval{Mode: draft.ModeOf(m), Window: w}
}

// kindApproval is a's approval of the gates of kind k, a kind but draft:
// its rules for k, then its mode, else gates.toml's default for k.
func (a *gateApproval) kindApproval(k gate.Kind, gates GatesDef) workflow.Approval {
	out := DefaultApproval(k, gates)
	if a == nil {
		return out
	}
	for _, r := range a.rules {
		if !r.appliesTo(k) {
			continue
		}
		switch static, holds := r.holdsFor(k); {
		case static && holds:
			out.Mode, out.Window = workflow.ModeOf(r.mode), r.window
			return out
		case !static:
			out.Rules = append(out.Rules, workflow.ApprovalRule{When: gatePredicate{r.unit, k}, Mode: workflow.ModeOf(r.mode), Window: r.window})
		}
	}
	if a.mode != nil && a.mode.appliesTo(k) {
		out.Mode, out.Window = workflow.ModeOf(a.mode.mode), a.mode.window
	}
	return out
}

// DefaultApproval is gates.toml's default for a gate of kind k, a kind but
// draft, as a run approval.
func DefaultApproval(k gate.Kind, gates GatesDef) workflow.Approval {
	m, w, _ := gates.DefaultFor(k)
	return workflow.Approval{Mode: workflow.ModeOf(m), Window: w}
}

// GateKinds are the kinds of the gates a workflow's gate.open steps open:
// every kind but draft and plan, which its drafts and runs are.
var GateKinds = slices.DeleteFunc(slices.Clone(gate.Kinds), func(k gate.Kind) bool { return k == gate.KindDraft || k == gate.KindPlan })

// gatePredicate is a rule's when over a unit's facts, read with the facts
// of a gate of its kind unless the caller gives the gate's own.
type gatePredicate struct {
	p    workflow.Predicate
	kind gate.Kind
}

func (g gatePredicate) Match(v workflow.Vars) (bool, error) {
	v.Facts = withGate(v.Facts, g.kind)
	return g.p.Match(v)
}

func (g gatePredicate) Reads() []string { return g.p.Reads() }

func (g gatePredicate) MarkNames() []string { return workflow.MarkNames(g.p) }

// Source is the when as the manifest wrote it.
func (g gatePredicate) Source() string {
	if s, ok := g.p.(interface{ Source() string }); ok {
		return s.Source()
	}
	return ""
}

// gateCondition is a draft rule's when, read with a draft gate's facts.
type gateCondition struct {
	c    draft.Condition
	kind gate.Kind
}

func (g gateCondition) Holds(facts map[string]any, now time.Time) (bool, error) {
	return g.c.Holds(withGate(facts, g.kind), now)
}

// Source is the when as the manifest wrote it.
func (g gateCondition) Source() string {
	if s, ok := g.c.(interface{ Source() string }); ok {
		return s.Source()
	}
	return ""
}

// withGate is facts with the facts of an open gate of kind k where facts
// has none of its own.
func withGate(facts map[string]any, k gate.Kind) map[string]any {
	out := map[string]any(GateFacts(gate.Gate{Kind: k, Status: gate.Open}, ""))
	maps.Copy(out, facts)
	return out
}

// untrustedRead is the first fact p reads that no releasing rule may: the
// table marks it untrusted, or env does.
func untrustedRead(p workflow.Predicate, env expr.Env) string {
	for _, name := range p.Reads() {
		if builtin.IsUntrusted(name) || env.Untrusted(name) {
			return name
		}
	}
	return ""
}

// ApprovalOverlay is the configuration's [gates.approval] of a workflow,
// laid over its manifest's field by field: a set Mode or Window replaces
// that field, and non-nil Rules replace the rules, an empty list clearing
// them. A Mode other than delay drops the manifest's window, which JSON
// cannot unset.
type ApprovalOverlay struct {
	Mode   string
	Window *time.Duration
	Rules  []RuleOverlay
}

// RuleOverlay is one rule of an ApprovalOverlay.
type RuleOverlay struct {
	When, Mode string
	Window     time.Duration
}

func (o ApprovalOverlay) modeWindow(mode string, window time.Duration) (string, time.Duration) {
	if o.Mode != "" {
		if mode = o.Mode; mode != string(gate.ModeDelay) {
			window = 0
		}
	}
	if o.Window != nil {
		window = *o.Window
	}
	return mode, window
}

// ApprovalEnvs are what an overlay rule of a workflow reads: Draft over a
// draft gate's facts and its unit's, Unit over its unit's for any other
// kind.
type ApprovalEnvs struct{ Draft, Unit expr.Env }

// overlayRule is r, an overlay rule, for the gates of every kind it applies
// to, as a manifest's rule.
func overlayRule(r RuleOverlay, envs ApprovalEnvs) (gateRule, error) {
	rule, _, err := compileRule(r.When, gate.Mode(r.Mode), r.Window, gate.Kinds, envs.Draft, envs.Unit)
	return rule, err
}

// OverlayRunApproval is a, a workflow's approval of its gates of kind k (a
// kind but draft), with o laid over it, o's rules for k compiled over envs.
// A mode that does not apply to k leaves a's. The merged approval is
// validated, and a releasing rule of o may not read an untrusted fact.
func OverlayRunApproval(a workflow.Approval, k gate.Kind, o ApprovalOverlay, envs ApprovalEnvs) (workflow.Approval, error) {
	if o.Mode == "" || gate.Mode(o.Mode).AppliesTo(k) {
		mode, window := o.modeWindow(string(a.Mode), a.Window)
		if o.Mode != "" {
			mode = string(workflow.ModeOf(gate.Mode(mode)))
		}
		a.Mode, a.Window = workflow.ApprovalMode(mode), window
	}
	if o.Rules != nil {
		a.Rules = make([]workflow.ApprovalRule, 0, len(o.Rules))
		for i, r := range o.Rules {
			rule, err := overlayRule(r, envs)
			if err != nil {
				return workflow.Approval{}, fmt.Errorf("rule[%d].when: %w", i, err)
			}
			if rule.appliesTo(k) {
				a.Rules = append(a.Rules, workflow.ApprovalRule{When: gatePredicate{rule.unit, k}, Mode: workflow.ModeOf(rule.mode), Window: rule.window})
			}
		}
	}
	return a, a.Validate()
}

// OverlayDraftApproval is base, a workflow's draft policy (nil: def,
// gates.toml's default for a draft gate), with o laid over it, o's rules
// for draft gates compiled over envs. The merged policy is validated.
func OverlayDraftApproval(base *draft.Policy, def draft.Approval, o ApprovalOverlay, envs ApprovalEnvs) (draft.Policy, error) {
	p := draft.Policy{Default: def}
	if base != nil {
		p = *base
	}
	if o.Mode == "" || gate.Mode(o.Mode).AppliesTo(gate.KindDraft) {
		mode, window := o.modeWindow(string(p.Default.Mode), p.Default.Window)
		if o.Mode != "" {
			mode = string(draft.ModeOf(gate.Mode(mode)))
		}
		p.Default = draft.Approval{Mode: draft.ApprovalMode(mode), Window: window}
	}
	if err := p.Default.Validate(); err != nil {
		return draft.Policy{}, err
	}
	if o.Rules != nil {
		p.Rules = make([]draft.Rule, 0, len(o.Rules))
		for i, r := range o.Rules {
			if err := (draft.Approval{Mode: draft.ModeOf(gate.Mode(r.Mode)), Window: r.Window}).Validate(); err != nil {
				return draft.Policy{}, fmt.Errorf("rule[%d]: %w", i, err)
			}
			rule, err := overlayRule(r, envs)
			if err != nil {
				return draft.Policy{}, fmt.Errorf("rule[%d].when: %w", i, err)
			}
			if rule.appliesTo(gate.KindDraft) {
				p.Rules = append(p.Rules, rule.draft)
			}
		}
	}
	return p, nil
}
