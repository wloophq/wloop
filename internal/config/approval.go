package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

type ApprovalRuleConfig struct {
	When   string `json:"when"`
	Mode   string `json:"mode"`
	Window Days   `json:"window,omitempty"`
}

// PluginDrafts holds the approval of the drafts a plugin's own rules
// propose.
type PluginDrafts struct {
	Approval *PluginApproval `json:"approval,omitempty"`
}

type PluginApproval struct {
	Mode   string `json:"mode"`
	Window Days   `json:"window,omitempty"`
}

// Approval is a as a draft's approval.
func (a PluginApproval) Approval() draft.Approval {
	return draft.Approval{Mode: draft.ApprovalMode(a.Mode), Window: time.Duration(a.Window)}
}

var pluginDraftsType = reflect.TypeFor[PluginDrafts]()

// PluginDraftApproval is the approval of the drafts plugin id's own rules
// propose: its <id>.drafts.approval, wait when the config sets none.
func (c Config) PluginDraftApproval(id string) draft.Approval {
	if a := c.pluginDrafts()[id].Approval; a != nil {
		return a.Approval()
	}
	return draft.Approval{Mode: draft.Wait}
}

// pluginDrafts are the <id>.drafts of each top-level table that has one.
func (c Config) pluginDrafts() map[string]PluginDrafts {
	out := map[string]PluginDrafts{}
	v := reflect.ValueOf(c)
	for i := range v.NumField() {
		f := v.Type().Field(i)
		name := jsonName(f)
		if f.Type.Kind() != reflect.Struct || name == "" || name == "-" {
			continue
		}
		if d := v.Field(i).FieldByName("Drafts"); d.IsValid() && d.Type() == pluginDraftsType {
			out[name] = d.Interface().(PluginDrafts)
		}
	}
	return out
}

func (c Config) validatePluginDrafts() error {
	drafts := c.pluginDrafts()
	for _, id := range slices.Sorted(maps.Keys(drafts)) {
		if a := drafts[id].Approval; a != nil {
			if err := a.Approval().Validate(); err != nil {
				return fmt.Errorf("%s.drafts.approval: %w", id, err)
			}
		}
	}
	return nil
}

// DraftApproval is the workflow's drafts.approval, nil when unset.
func (w WorkflowConfig) DraftApproval() *ApprovalConfig {
	if w.Drafts == nil {
		return nil
	}
	return w.Drafts.Approval
}

var runModes = []string{string(workflow.OutcomeRun), "delay", string(workflow.OutcomeWait)}

// validateRun checks a run approval block at key: its modes, and its rules'
// predicates over the workflow facts.
func (a ApprovalConfig) validateRun(key string) error {
	if err := oneOf(key+".mode", a.Mode, runModes...); err != nil {
		return err
	}
	for i, r := range a.Rule {
		at := fmt.Sprintf("%s.rule[%d]", key, i)
		if r.Mode == "" {
			return fmt.Errorf("%s.mode is missing; one of %v", at, runModes)
		}
		if err := oneOf(at+".mode", r.Mode, runModes...); err != nil {
			return err
		}
		if _, err := expr.CompileWorkflow(r.When, builtin.WorkflowEnvAll()); err != nil {
			return fmt.Errorf("%s.when: %w", at, err)
		}
	}
	return nil
}

// Policy is a as a draft approval policy, its rules' when compiled over env.
func (a ApprovalConfig) Policy(env expr.Env) (draft.Policy, error) {
	p := draft.Policy{Default: draft.Approval{Mode: draft.ApprovalMode(a.Mode), Window: a.window()}}
	if err := p.Default.Validate(); err != nil {
		return draft.Policy{}, err
	}
	for i, r := range a.Rule {
		ra := draft.Approval{Mode: draft.ApprovalMode(r.Mode), Window: time.Duration(r.Window)}
		if err := ra.Validate(); err != nil {
			return draft.Policy{}, fmt.Errorf("rule[%d]: %w", i, err)
		}
		rule, err := builtin.ApprovalRule(r.When, ra, env)
		if err != nil {
			return draft.Policy{}, fmt.Errorf("rule[%d].when: %w", i, err)
		}
		p.Rules = append(p.Rules, rule)
	}
	return p, nil
}

func (a ApprovalConfig) window() time.Duration {
	if a.Window == nil {
		return 0
	}
	return time.Duration(*a.Window)
}

// DraftApprovalWarnings name each rule of a workflows.<name>.drafts.approval
// that releases, reads draft.delivery and never releases a comment draft.
func (c Config) DraftApprovalWarnings() []string {
	var out []string
	env := builtin.ApprovalEnv(true, builtin.UnitSlots()...)
	for _, name := range slices.Sorted(maps.Keys(c.Workflows)) {
		a := c.Workflows[name].DraftApproval()
		if a == nil {
			continue
		}
		p, err := a.Policy(env)
		if err != nil {
			continue
		}
		for i, r := range p.Rules {
			if builtin.NeverReleasesComment(r) {
				out = append(out, fmt.Sprintf("workflows.%s.drafts.approval.rule[%d].when reads draft.delivery but never releases a comment draft; to release one, write %s", name, i, builtin.CommentForm))
			}
		}
	}
	return out
}
