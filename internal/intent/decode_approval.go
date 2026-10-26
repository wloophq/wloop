package intent

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// mApproval is the approval table of runs ([approval]) and of drafts
// ([drafts.approval]): one shape, a mode set per object.
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

// draftApprovalAt is the draft policy of m at at, its rules' when compiled
// over env; nil when m is, wait when m sets no mode. A rule that releases while reading draft.delivery
// in a form that never names comment delivery loads with a warning.
func (s *sheet) draftApprovalAt(at string, m *mApproval, env expr.Env) *draft.Policy {
	if m == nil {
		return nil
	}
	mode := m.Mode
	if mode == nil {
		wait := string(draft.Wait)
		mode = &wait
	}
	a, ok := s.approval(at, mode, m.Window)
	if !ok {
		return nil
	}
	p := &draft.Policy{Default: a}
	for i, r := range m.Rule {
		path := fmt.Sprintf("%s.rule[%d]", at, i)
		ra, ok := s.approval(path, r.Mode, r.Window)
		if r.When == nil {
			s.add(path+".when", "is required: a predicate over the draft's facts and its unit's")
			ok = false
		}
		if !ok {
			continue
		}
		rule, err := builtin.ApprovalRule(*r.When, ra, env)
		if err != nil {
			s.ruleProblem(path+".when", *r.When, err, env)
			continue
		}
		if builtin.NeverReleasesComment(rule) {
			s.warn(path+".when", "reads draft.delivery but never releases a comment draft; to release one, write %s", builtin.CommentForm)
		}
		p.Rules = append(p.Rules, rule)
	}
	return p
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
	s.problems[len(s.problems)-1].Snippet = snippet
}

// approval is a draft's mode and window at path.
func (s *sheet) approval(path string, mode, window *string) (draft.Approval, bool) {
	m, w, ok := s.modeWindow(path, mode, window, builtin.DraftModes(), "the draft is sent")
	return draft.Approval{Mode: draft.ApprovalMode(m), Window: w}, ok
}

// modeWindow is the mode, one of modes, and the window at path: a window
// only with delay, which needs one.
func (s *sheet) modeWindow(path string, mode, window *string, modes []builtin.Value, what string) (string, time.Duration, bool) {
	if mode == nil {
		s.add(path+".mode", "is required: one of %s", quoted(builtin.ValueNames(modes)))
		return "", 0, false
	}
	delay := *mode == string(draft.Delay)
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

// runApproval is a workflow's [approval] at approval, its rules' when
// compiled over env: run when m is nil or sets no mode. A rule that
// releases (run or delay) may not read an untrusted fact; a wait rule may,
// since it only holds an item.
func (s *sheet) runApproval(m *mApproval, env expr.Env) workflow.Approval {
	a := workflow.Approval{Mode: workflow.ApprovalRun}
	if m == nil {
		return a
	}
	mode := m.Mode
	if mode == nil {
		run := string(workflow.ApprovalRun)
		mode = &run
	}
	if v, w, ok := s.modeWindow("approval", mode, m.Window, builtin.RunModes(), "the item runs"); ok {
		a.Mode, a.Window = workflow.ApprovalMode(v), w
	}
	for i, r := range m.Rule {
		path := fmt.Sprintf("approval.rule[%d]", i)
		v, w, ok := s.modeWindow(path, r.Mode, r.Window, builtin.RunModes(), "the item runs")
		if r.When == nil {
			s.add(path+".when", "is required: a predicate over the unit's facts, the trigger's and config.*")
			continue
		}
		if !ok {
			continue
		}
		rule := workflow.ApprovalRule{Mode: workflow.ApprovalMode(v), Window: w}
		if env == nil {
			continue
		}
		n := len(s.problems)
		if rule.When = s.predicate(path+".when", *r.When, env); len(s.problems) > n {
			continue
		}
		if rule.Mode.Releases() {
			if f := untrustedRead(rule.When); f != "" {
				s.add(path+".when", "reads %s, which a rule that runs (mode %s) may not: a third party's text or an agent sets its value; a wait rule may read it", f, rule.Mode)
				continue
			}
		}
		a.Rules = append(a.Rules, rule)
	}
	return a
}

func untrustedRead(p workflow.Predicate) string {
	for _, name := range p.Reads() {
		if builtin.IsUntrusted(name) {
			return name
		}
	}
	return ""
}

// ApprovalOverlay is the configuration's approval block of a workflow, laid
// over its manifest's field by field: a set Mode or Window replaces that
// field, and non-nil Rules replace the rules, an empty list clearing them.
// A Mode other than delay drops the manifest's window, which JSON cannot
// unset.
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
		if mode = o.Mode; mode != string(draft.Delay) {
			window = 0
		}
	}
	if o.Window != nil {
		window = *o.Window
	}
	return mode, window
}

// OverlayRunApproval is a, a workflow's run approval, with o laid over it,
// o's rules compiled over env, the workflow's facts. The merged approval
// is validated, and a releasing rule of o may not read an untrusted fact.
func OverlayRunApproval(a workflow.Approval, o ApprovalOverlay, env expr.Env) (workflow.Approval, error) {
	mode, window := o.modeWindow(string(a.Mode), a.Window)
	a.Mode, a.Window = workflow.ApprovalMode(mode), window
	if o.Rules != nil {
		a.Rules = make([]workflow.ApprovalRule, 0, len(o.Rules))
		for i, r := range o.Rules {
			p, err := expr.CompileWorkflow(r.When, env)
			if err != nil {
				return workflow.Approval{}, fmt.Errorf("rule[%d].when: %w", i, err)
			}
			rule := workflow.ApprovalRule{When: p, Mode: workflow.ApprovalMode(r.Mode), Window: r.Window}
			if f := untrustedRead(p); f != "" && rule.Mode.Releases() {
				return workflow.Approval{}, fmt.Errorf("rule[%d].when reads %s, which a rule that runs (mode %s) may not", i, f, rule.Mode)
			}
			a.Rules = append(a.Rules, rule)
		}
	}
	return a, a.Validate()
}

// OverlayDraftApproval is base, a workflow's draft policy (nil: wait), with
// o laid over it, o's rules compiled over env. The merged policy is
// validated.
func OverlayDraftApproval(base *draft.Policy, o ApprovalOverlay, env expr.Env) (draft.Policy, error) {
	p := draft.Policy{Default: draft.Approval{Mode: draft.Wait}}
	if base != nil {
		p = *base
	}
	mode, window := o.modeWindow(string(p.Default.Mode), p.Default.Window)
	p.Default = draft.Approval{Mode: draft.ApprovalMode(mode), Window: window}
	if err := p.Default.Validate(); err != nil {
		return draft.Policy{}, err
	}
	if o.Rules != nil {
		p.Rules = make([]draft.Rule, 0, len(o.Rules))
		for i, r := range o.Rules {
			ra := draft.Approval{Mode: draft.ApprovalMode(r.Mode), Window: r.Window}
			if err := ra.Validate(); err != nil {
				return draft.Policy{}, fmt.Errorf("rule[%d]: %w", i, err)
			}
			rule, err := builtin.ApprovalRule(r.When, ra, env)
			if err != nil {
				return draft.Policy{}, fmt.Errorf("rule[%d].when: %w", i, err)
			}
			p.Rules = append(p.Rules, rule)
		}
	}
	return p, nil
}
