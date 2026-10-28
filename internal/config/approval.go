package config

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

type ApprovalRuleConfig struct {
	When   string `json:"when"`
	Mode   string `json:"mode"`
	Window Days   `json:"window,omitempty"`
}

// PluginGates holds the approval of the gates a plugin's own rules open.
type PluginGates struct {
	Approval *PluginApproval `json:"approval,omitempty"`
}

type PluginApproval struct {
	Mode   string `json:"mode"`
	Window Days   `json:"window,omitempty"`
}

// Approval is a as a draft's approval.
func (a PluginApproval) Approval() draft.Approval {
	return draft.Approval{Mode: draft.ModeOf(gate.Mode(a.Mode)), Window: time.Duration(a.Window)}
}

var pluginGatesType = reflect.TypeFor[PluginGates]()

// PluginDraftApproval is the approval of the drafts plugin id's own rules
// propose: its plugins.<id>.gates.approval; false when the config sets
// none, and the profile's default applies.
func (c Config) PluginDraftApproval(id string) (draft.Approval, bool) {
	if a := c.pluginGates()[id].Approval; a != nil {
		return a.Approval(), true
	}
	return draft.Approval{}, false
}

// pluginGates are the gates of each bundled plugin's table.
func (c Config) pluginGates() map[string]PluginGates {
	out := map[string]PluginGates{}
	for id, f := range c.Plugins.bundled() {
		if g := f.FieldByName("Gates"); g.IsValid() && g.Type() == pluginGatesType {
			out[id] = g.Interface().(PluginGates)
		}
	}
	return out
}

func (c Config) validatePluginDrafts() error {
	gates := c.pluginGates()
	for _, id := range slices.Sorted(maps.Keys(gates)) {
		if a := gates[id].Approval; a != nil {
			if err := oneOf("plugins."+id+".gates.approval.mode", a.Mode, gateModes...); err != nil {
				return err
			}
			if err := a.Approval().Validate(); err != nil {
				return fmt.Errorf("plugins.%s.gates.approval: %w", id, err)
			}
		}
	}
	return nil
}

// GateApproval is the workflow's gates.approval, nil when unset.
func (w WorkflowConfig) GateApproval() *ApprovalConfig {
	if w.Gates == nil {
		return nil
	}
	return w.Gates.Approval
}

var gateModes = builtin.ValueNames(builtin.GateModes())

// validateGates checks a gates approval overlay at key as far as it holds
// without the manifest it overlays: what it sets, and each of its rules
// whole, over a draft's facts, every unit's and the gate's. A rule that
// releases a gate (send, run or delay) may not read an untrusted fact; one
// that reads task.prop.* is checked where the profile is loaded, which
// says who writes the property, unless it releases: config.toml refuses
// that here. dry_run pinned to a kind but draft is refused. The merged
// block is checked where the profile is loaded.
func (a ApprovalConfig) validateGates(key string) error {
	if err := a.validateOverlay(key, gateModes); err != nil {
		return err
	}
	env := gateRuleEnv()
	for i, r := range a.Rule {
		at := fmt.Sprintf("%s.rule[%d]", key, i)
		if err := r.validate(at, gateModes); err != nil {
			return err
		}
		releases := gate.Mode(r.Mode) != gate.ModeWait
		if names := builtin.DynamicFacts(r.When); len(names) > 0 {
			prop, ok := onlyProps(r.When)
			switch {
			case ok && releases:
				return fmt.Errorf("%s.when reads %s: config.toml cannot see who writes it, the profile declares it, so a rule that releases a gate here may not read it; a wait rule may", at, prop)
			case releases:
				return fmt.Errorf("%s.when reads %s, which a rule that releases a gate may not: a third party's text or an agent sets its value; a wait rule may read it", at, names[0])
			case !ok && !pinnedAwayFromDrafts(r.When, names, env):
				return fmt.Errorf("%s.when reads %s, which no rule of a draft gate may: %s", at, names[0], reachesDrafts)
			}
			continue
		}
		p, err := expr.CompileWorkflow(r.When, env)
		if err != nil {
			return fmt.Errorf("%s.when: %w", at, err)
		}
		var subjects *builtin.SubjectsError
		var untrusted *builtin.UntrustedError
		_, err = builtin.ApprovalRule(r.When, draft.Approval{Mode: draft.ModeOf(gate.Mode(r.Mode)), Window: time.Duration(r.Window)}, env)
		switch {
		case errors.As(err, &subjects):
			return fmt.Errorf("%s.when: %w", at, err)
		case errors.As(err, &untrusted) && !releases && !pinsAwayFromDrafts(p):
			return fmt.Errorf("%s.when: %w; %s", at, err, reachesDrafts)
		}
		if gate.Mode(r.Mode) == gate.ModeDryRun {
			for _, k := range gate.Kinds {
				if k != gate.KindDraft && expr.Requires(p, builtin.GateSlot+".kind", string(k)) {
					return fmt.Errorf("%s.when pins gate.kind to %s with mode dry_run, which applies to draft gates only", at, k)
				}
			}
		}
		if releases {
			for _, name := range p.Reads() {
				if builtin.IsUntrusted(name) {
					return fmt.Errorf("%s.when reads %s, which a rule that releases a gate may not: a third party's text or an agent sets its value; a wait rule may read it", at, name)
				}
			}
		}
	}
	return nil
}

// reachesDrafts is why a wait rule over an untrusted fact that pins no
// other kind is refused: rules are tried in order, so dropping it from draft
// gates would let a later rule release the draft it meant to hold.
const reachesDrafts = `it reaches draft gates, where a later rule would decide in its place; pin it to the other kinds, such as gate.kind == "plan"`

// pinsAwayFromDrafts says whether p pins gate.kind to kinds that are not draft.
func pinsAwayFromDrafts(p workflow.Predicate) bool {
	pinned := false
	for _, k := range gate.Kinds {
		if expr.Requires(p, builtin.GateSlot+".kind", string(k)) {
			if k == gate.KindDraft {
				return false
			}
			pinned = true
		}
	}
	return pinned
}

// pinnedAwayFromDrafts is pinsAwayFromDrafts of src, whose facts names only
// the profile types: each is tried as each type a fact may take.
func pinnedAwayFromDrafts(src string, names []string, env expr.Env) bool {
	for _, t := range []expr.Type{expr.Bool, expr.String, expr.Int, expr.List, expr.Time, expr.Duration} {
		with := maps.Clone(env)
		for _, n := range names {
			with[n] = t
		}
		if p, err := expr.CompileWorkflow(src, with); err == nil {
			return pinsAwayFromDrafts(p)
		}
	}
	return false
}

// gateRuleEnv is what a config rule of [gates.approval] may read: a
// draft's facts, every unit's, the trigger's, the config's and the gate's.
func gateRuleEnv() expr.Env {
	env := builtin.WorkflowEnvAll()
	maps.Copy(env, builtin.ApprovalEnv(true, builtin.UnitSlots()...))
	return env
}

// validateOverlay refuses a mode outside modes and a window beside a mode
// other than delay; a window alone, or delay alone, waits for the merge.
func (a ApprovalConfig) validateOverlay(key string, modes []string) error {
	if err := oneOf(key+".mode", a.Mode, modes...); err != nil {
		return err
	}
	switch {
	case a.Window != nil && *a.Window <= 0:
		return fmt.Errorf("%s.window must be positive, got %s", key, time.Duration(*a.Window))
	case a.Window != nil && a.Mode != "" && a.Mode != string(gate.ModeDelay):
		return fmt.Errorf("%s.window is read only with mode delay, not with %s", key, a.Mode)
	}
	return nil
}

// validate checks a rule's mode, one of modes, and its window: one with
// delay, none otherwise.
func (r ApprovalRuleConfig) validate(at string, modes []string) error {
	if r.Mode == "" {
		return fmt.Errorf("%s.mode is missing; one of %s", at, strings.Join(modes, ", "))
	}
	if err := oneOf(at+".mode", r.Mode, modes...); err != nil {
		return err
	}
	switch delay := r.Mode == string(gate.ModeDelay); {
	case delay && r.Window <= 0:
		return fmt.Errorf("%s.window is required with mode delay", at)
	case !delay && r.Window != 0:
		return fmt.Errorf("%s.window is read only with mode delay, not with %s", at, r.Mode)
	}
	return nil
}

// workflowRule checks a predicate of a workflow's config over the workflow
// facts. One that names a fact the profile declares (brief.extra.*,
// classify.*) is checked where the profile is loaded.
func workflowRule(at, src string) error {
	if len(builtin.DynamicFacts(src)) > 0 {
		return nil
	}
	if _, err := expr.CompileWorkflow(src, builtin.WorkflowEnvAll()); err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	return nil
}

// onlyProps is the first task.prop.* fact src names when every fact only
// the profile types that it names is a task property.
func onlyProps(src string) (string, bool) {
	names := builtin.DynamicFacts(src)
	for _, n := range names {
		if !strings.HasPrefix(n, builtin.PropPrefix) {
			return "", false
		}
	}
	if len(names) == 0 {
		return "", false
	}
	return names[0], true
}

// GateApprovalWarnings name each rule of a workflows.<name>.gates.approval
// that releases, reads draft.delivery and names no delivery, so it never
// releases a draft whose delivery posts.
func (c Config) GateApprovalWarnings() []string {
	var out []string
	env := builtin.ApprovalEnv(true, builtin.UnitSlots()...)
	for _, name := range slices.Sorted(maps.Keys(c.Workflows)) {
		a := c.Workflows[name].GateApproval()
		if a == nil {
			continue
		}
		for i, rc := range a.Rule {
			r, err := builtin.ApprovalRule(rc.When, draft.Approval{Mode: draft.ModeOf(gate.Mode(rc.Mode)), Window: time.Duration(rc.Window)}, env)
			if err == nil && builtin.NeverNamesADelivery(r) {
				out = append(out, fmt.Sprintf("workflows.%s.gates.approval.rule[%d].when reads draft.delivery but names none, so it never releases a draft whose delivery posts; to release one, write %s", name, i, builtin.DeliveryForm))
			}
		}
	}
	return out
}
