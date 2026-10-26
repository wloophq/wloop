package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// Proposals holds the settings of the drafts proposed on their own: how long a nudged, discarded, sent or
// observed proposal keeps its rule or workflow from proposing it again, and a plugin rule's approval by rule id
// ("github-nudge"). In JSON the rules sit beside "renudge_after". The rules the default workflows replaced
// ("stalled-nudge", "closure", "ask-owner") still load, their approval read as their workflow's draft_approval.
type Proposals struct {
	RenudgeAfter Days
	Rules        map[string]ProposalRule
}

type ProposalRule struct {
	Approval *ProposalApproval `json:"approval,omitempty"`
}

type ProposalApproval struct {
	Mode   string `json:"mode"`
	Window Days   `json:"window,omitempty"`
}

// DraftApprovalConfig is a workflow's draft_approval: a mode, and the rules
// tried before it, first match wins.
type DraftApprovalConfig struct {
	Mode   string               `json:"mode"`
	Window Days                 `json:"window,omitempty"`
	Rule   []ApprovalRuleConfig `json:"rule,omitempty"`
}

type ApprovalRuleConfig struct {
	When   string `json:"when"`
	Mode   string `json:"mode"`
	Window Days   `json:"window,omitempty"`
}

// Policy is a as a draft approval policy, its rules' when compiled over env.
func (a DraftApprovalConfig) Policy(env expr.Env) (draft.Policy, error) {
	p := draft.Policy{Default: draft.Approval{Mode: draft.ApprovalMode(a.Mode), Window: time.Duration(a.Window)}}
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

// DraftApprovalWarnings name each rule of a workflows.<name>.draft_approval
// that releases, reads draft.delivery and never releases a comment draft.
func (c Config) DraftApprovalWarnings() []string {
	var out []string
	env := builtin.ApprovalEnv(true, builtin.UnitSlots()...)
	for _, name := range slices.Sorted(maps.Keys(c.Workflows)) {
		a := c.Workflows[name].DraftApproval
		if a == nil {
			continue
		}
		p, err := a.Policy(env)
		if err != nil {
			continue
		}
		for i, r := range p.Rules {
			if builtin.NeverReleasesComment(r) {
				out = append(out, fmt.Sprintf("workflows.%s.draft_approval.rule[%d].when reads draft.delivery but never releases a comment draft; to release one, write %s", name, i, builtin.CommentForm))
			}
		}
	}
	return out
}

const renudgeAfterKey = "renudge_after"

// Approval is rule's configured approval, wait when the config sets none.
func (p Proposals) Approval(rule string) draft.Approval {
	r, ok := p.Rules[rule]
	if !ok || r.Approval == nil {
		return draft.Approval{Mode: draft.Wait}
	}
	return r.Approval.Approval()
}

// Approval is a as a draft's approval.
func (a ProposalApproval) Approval() draft.Approval {
	return draft.Approval{Mode: draft.ApprovalMode(a.Mode), Window: time.Duration(a.Window)}
}

func (p *Proposals) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		if k == renudgeAfterKey {
			if err := json.Unmarshal(raw[k], &p.RenudgeAfter); err != nil {
				return fmt.Errorf("proposals.%s: %w", k, err)
			}
			continue
		}
		var r ProposalRule
		if err := json.Unmarshal(raw[k], &r); err != nil {
			return fmt.Errorf("proposals.%s: %w", k, err)
		}
		if p.Rules == nil {
			p.Rules = map[string]ProposalRule{}
		}
		p.Rules[k] = r
	}
	return nil
}

func (p Proposals) MarshalJSON() ([]byte, error) {
	out := map[string]any{renudgeAfterKey: p.RenudgeAfter}
	for k, r := range p.Rules {
		out[k] = r
	}
	return json.Marshal(out)
}

func (p Proposals) validate() error {
	if p.RenudgeAfter <= 0 {
		return fmt.Errorf("proposals.%s must be positive, got %s", renudgeAfterKey, time.Duration(p.RenudgeAfter))
	}
	for _, rule := range slices.Sorted(maps.Keys(p.Rules)) {
		if err := p.Approval(rule).Validate(); err != nil {
			return fmt.Errorf("proposals.%s.approval: %w", rule, err)
		}
	}
	return nil
}

// legacyDraftWorkflows are the proposals job's rules a default workflow
// replaced, by rule id, and that workflow.
var legacyDraftWorkflows = map[string]string{
	"stalled-nudge": "slack-stalled-nudge",
	"closure":       "slack-closure",
	"ask-owner":     "slack-ask-owner",
}

// LegacyDraftApproval is the proposals.<rule>.approval of the rule the
// workflow replaced, nil when the config sets none.
func (p Proposals) LegacyDraftApproval(workflow string) *draft.Policy {
	for rule, w := range legacyDraftWorkflows {
		if r, ok := p.Rules[rule]; ok && w == workflow && r.Approval != nil {
			return draft.Fixed(r.Approval.Approval())
		}
	}
	return nil
}

// LegacyWarnings name the key that replaced each proposals.<rule>.approval
// the config still sets for a rule a workflow replaced.
func (p Proposals) LegacyWarnings() []string {
	var out []string
	for _, rule := range slices.Sorted(maps.Keys(legacyDraftWorkflows)) {
		if r, ok := p.Rules[rule]; ok && r.Approval != nil {
			out = append(out, fmt.Sprintf("proposals.%s.approval is read as workflows.%s.draft_approval; move it there", rule, legacyDraftWorkflows[rule]))
		}
	}
	return out
}
