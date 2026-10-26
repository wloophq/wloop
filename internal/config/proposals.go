package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// Proposals holds the settings of the rules that propose drafts on their own: how long a nudged, discarded,
// sent or observed proposal keeps its rule from proposing it again, and each rule's approval by rule id
// ("stalled-nudge", "closure", "ask-owner", "github-nudge"). In JSON the rules sit beside "renudge_after".
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

const renudgeAfterKey = "renudge_after"

// Approval is rule's configured approval, always when the config sets none.
func (p Proposals) Approval(rule string) draft.Approval {
	r, ok := p.Rules[rule]
	if !ok || r.Approval == nil {
		return draft.Approval{Mode: draft.Always}
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
