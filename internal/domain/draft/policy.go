package draft

import (
	"fmt"
	"time"
)

// Policy is the approval of a proposer's drafts: the first Rule whose When
// holds, else Default.
type Policy struct {
	Default Approval
	Rules   []Rule
}

// Rule is one [draft_approval] rule.
type Rule struct {
	When     Condition
	Approval Approval
	// NamesComment says that When holds only for a draft whose delivery is
	// comment, so the rule may release one.
	NamesComment bool
}

// Condition is a rule's compiled when over the facts RuleFacts and the
// unit's facts give.
type Condition interface {
	Holds(facts map[string]any, now time.Time) (bool, error)
}

// Fixed is the policy of a, with no rules.
func Fixed(a Approval) *Policy { return &Policy{Default: a} }

// Validate refuses a policy whose default or a rule's approval does not
// validate, or a rule with no When.
func (p Policy) Validate() error {
	if err := p.Default.Validate(); err != nil {
		return err
	}
	for i, r := range p.Rules {
		if r.When == nil {
			return fmt.Errorf("draft approval rule %d has no when", i)
		}
		if err := r.Approval.Validate(); err != nil {
			return fmt.Errorf("draft approval rule %d: %w", i, err)
		}
	}
	return nil
}

// Releases says whether a sends a draft with no person approving it.
func (a Approval) Releases() bool { return a.Mode == None || a.Mode == Window }

// Decision is the approval Decide picked and why.
type Decision struct {
	Approval Approval
	// Rule is the index of the rule that matched; -1 for none.
	Rule int
	// CommentReleased says that a rule naming comment delivery released a
	// comment draft, the only way one is sent with no person approving it.
	CommentReleased bool
}

// Decide is d's approval under p: a rule that releases applies to a comment
// draft only when it names comment delivery, and a comment draft no such
// rule releases waits for a person unless the approval is observe.
func (p Policy) Decide(d Draft, facts map[string]any, now time.Time) (Decision, error) {
	comment := d.Delivery == CommentDelivery
	var all map[string]any
	for i, r := range p.Rules {
		if comment && r.Approval.Releases() && !r.NamesComment {
			continue
		}
		if all == nil {
			all = withRuleFacts(d, facts)
		}
		ok, err := r.When.Holds(all, now)
		if err != nil {
			return Decision{Approval: Approval{Mode: Always}, Rule: i}, fmt.Errorf("draft approval rule %d: %w", i, err)
		}
		if ok {
			return Decision{Approval: r.Approval, Rule: i, CommentReleased: comment && r.Approval.Releases()}, nil
		}
	}
	if comment && p.Default.Releases() {
		return Decision{Approval: Approval{Mode: Always}, Rule: -1}, nil
	}
	return Decision{Approval: p.Default, Rule: -1}, nil
}

func withRuleFacts(d Draft, facts map[string]any) map[string]any {
	out := make(map[string]any, len(facts)+4)
	for k, v := range facts {
		out[k] = v
	}
	for k, v := range d.RuleFacts() {
		out[k] = v
	}
	return out
}

// RuleFacts are the facts of d a rule reads, those no agent chooses: its
// delivery, its proposer and its effects, and its purpose only when a
// workflow or a rule of the loop named it.
func (d Draft) RuleFacts() map[string]any {
	purpose := ""
	if d.Origin.Kind == OriginAuto {
		purpose = d.Purpose
	}
	effects := append([]string{}, d.Effects...)
	return map[string]any{
		"draft.delivery": string(d.Delivery),
		"draft.proposer": d.Origin.Proposer(),
		"draft.effects":  effects,
		"draft.purpose":  purpose,
	}
}

// Proposer names what proposed a draft of o: workflow:<name>, rule:<name>,
// intent:<name> or user.
func (o Origin) Proposer() string {
	switch {
	case o.Kind == OriginAuto && o.Workflow != "":
		return "workflow:" + o.Workflow
	case o.Kind == OriginAuto:
		return "rule:" + o.Rule
	case o.Kind == OriginUser:
		return "user"
	case o.Intent != "":
		return "intent:" + o.Intent
	}
	return string(o.Kind)
}
