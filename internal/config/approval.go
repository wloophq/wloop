package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
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

var (
	runModes   = []string{string(workflow.ApprovalRun), string(workflow.ApprovalDelay), string(workflow.ApprovalWait)}
	draftModes = []string{string(draft.Send), string(draft.Delay), string(draft.Wait), string(draft.DryRun)}
)

// validateRun checks a run approval overlay at key as far as it holds
// without the manifest it overlays: what it sets, and each of its rules
// whole. A rule that runs (mode run or delay) may not read an untrusted
// fact. The merged block is checked where the profile is loaded.
func (a ApprovalConfig) validateRun(key string) error {
	if err := a.validateOverlay(key, runModes); err != nil {
		return err
	}
	for i, r := range a.Rule {
		at := fmt.Sprintf("%s.rule[%d]", key, i)
		if err := r.validate(at, runModes); err != nil {
			return err
		}
		if err := workflowRule(at+".when", r.When, workflow.ApprovalMode(r.Mode).Releases()); err != nil {
			return err
		}
	}
	return nil
}

// validateDrafts checks a draft approval overlay at key as validateRun
// does a run one; every draft rule refuses an untrusted fact.
func (a ApprovalConfig) validateDrafts(key string) error {
	if err := a.validateOverlay(key, draftModes); err != nil {
		return err
	}
	env := builtin.ApprovalEnv(true, builtin.UnitSlots()...)
	for i, r := range a.Rule {
		at := fmt.Sprintf("%s.rule[%d]", key, i)
		if err := r.validate(at, draftModes); err != nil {
			return err
		}
		if _, err := builtin.ApprovalRule(r.When, draft.Approval{Mode: draft.ApprovalMode(r.Mode), Window: time.Duration(r.Window)}, env); err != nil {
			return fmt.Errorf("%s.when: %w", at, err)
		}
	}
	return nil
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
	case a.Window != nil && a.Mode != "" && a.Mode != string(draft.Delay):
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
	switch delay := r.Mode == string(draft.Delay); {
	case delay && r.Window <= 0:
		return fmt.Errorf("%s.window is required with mode delay", at)
	case !delay && r.Window != 0:
		return fmt.Errorf("%s.window is read only with mode delay, not with %s", at, r.Mode)
	}
	return nil
}

// workflowRule checks a predicate of a workflow's config over the workflow
// facts; one that releases may not read an untrusted fact. One that names
// a fact the profile declares (brief.extra.*, classify.*) is checked where
// the profile is loaded.
func workflowRule(at, src string, releases bool) error {
	if names := builtin.DynamicFacts(src); len(names) > 0 {
		if releases {
			return fmt.Errorf("%s reads %s, which a rule that runs may not: a third party's text or an agent sets its value; a wait rule may read it", at, names[0])
		}
		return nil
	}
	p, err := expr.CompileWorkflow(src, builtin.WorkflowEnvAll())
	if err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	if releases {
		for _, name := range p.Reads() {
			if builtin.IsUntrusted(name) {
				return fmt.Errorf("%s reads %s, which a rule that runs may not: a third party's text or an agent sets its value; a wait rule may read it", at, name)
			}
		}
	}
	return nil
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
		for i, rc := range a.Rule {
			r, err := builtin.ApprovalRule(rc.When, draft.Approval{Mode: draft.ApprovalMode(rc.Mode), Window: time.Duration(rc.Window)}, env)
			if err == nil && builtin.NeverReleasesComment(r) {
				out = append(out, fmt.Sprintf("workflows.%s.drafts.approval.rule[%d].when reads draft.delivery but never releases a comment draft; to release one, write %s", name, i, builtin.CommentForm))
			}
		}
	}
	return out
}
