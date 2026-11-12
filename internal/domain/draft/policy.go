package draft

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
)

// Policy is the approval of a proposer's drafts: the first Rule whose When
// holds, else Default.
type Policy struct {
	Default Approval
	Rules   []Rule
	// Scope is the workflow whose scoped facts of the subject the rules
	// read; "" reads the unit's facts unscoped.
	Scope string
}

// Subject is the facts a policy's rules read beside the draft's own, and
// the slot they are of.
type Subject struct {
	Slot  string
	Facts map[string]any
	// Kind is the draft's kind as its plugin declares it, whose Facts are
	// the payload facts the rules read; the zero KindSpec gives none.
	Kind KindSpec
}

// Rule is one [gates.approval] rule of a draft gate.
type Rule struct {
	When     Condition
	Approval Approval
	// Names is the delivery When holds for alone, which its top-level
	// conjunction pins; "" for none. A rule releases a draft whose delivery
	// posts only when it names that delivery.
	Names Delivery
	// Subjects are the slots of the subject facts When reads; the rule
	// applies only to a draft whose subject is of that slot.
	Subjects []string
	// Payload are the payload facts When reads. A rule that releases never
	// applies to a draft that lacks one, and a draft whose own kind
	// declares one its payload does not hold waits.
	Payload []string
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

// ReadsSubject says whether a rule of p reads a fact of the draft's subject.
func (p Policy) ReadsSubject() bool {
	for _, r := range p.Rules {
		if len(r.Subjects) > 0 {
			return true
		}
	}
	return false
}

// Releases says whether a sends a draft with no person approving it.
func (a Approval) Releases() bool { return a.Mode.Releases() }

// Decision is the approval Decide picked and why.
type Decision struct {
	Approval Approval
	// Rule is the index of the rule that matched; -1 for none.
	Rule int
	// PostReleased says that a rule naming d's delivery released a draft
	// whose delivery posts, the only way one is sent with no person
	// approving it.
	PostReleased bool
}

// Decide is d's approval under p over the facts of its subject s; posts
// says that d's delivery reaches people, as its plugin's manifest declares
// it. A rule applies only when the facts it reads are of s's slot, a rule
// that releases applies to a draft that posts only when it names d's
// delivery, and a draft that posts no such rule releases waits for a
// person unless the approval is dry_run.
func (p Policy) Decide(d Draft, s Subject, posts bool, now time.Time) (Decision, error) {
	var all, payload map[string]any
	for i, r := range p.Rules {
		if posts && r.Approval.Releases() && r.Names != d.Delivery || !ofSlot(r.Subjects, s.Slot) {
			continue
		}
		if all == nil {
			if slices.ContainsFunc(p.Rules, func(r Rule) bool { return len(r.Payload) > 0 }) {
				payload = d.PayloadFacts(s.Kind)
			}
			all = withRuleFacts(d, s.Facts, payload)
		}
		if missing := absent(r.Payload, payload); len(missing) > 0 {
			if own := d.ownFacts(missing); own != "" {
				return Decision{Approval: Approval{Mode: Wait}, Rule: i}, fmt.Errorf("draft approval rule %d reads %s, which the draft's payload does not hold", i, own)
			}
			if r.Approval.Releases() {
				continue
			}
		}
		ok, err := r.When.Holds(all, now)
		if err != nil {
			return Decision{Approval: Approval{Mode: Wait}, Rule: i}, fmt.Errorf("draft approval rule %d: %w", i, err)
		}
		if ok {
			return Decision{Approval: r.Approval, Rule: i, PostReleased: posts && r.Approval.Releases()}, nil
		}
	}
	if posts && p.Default.Releases() {
		return Decision{Approval: Approval{Mode: Wait}, Rule: -1}, nil
	}
	return Decision{Approval: p.Default, Rule: -1}, nil
}

func ofSlot(subjects []string, slot string) bool {
	for _, s := range subjects {
		if s != slot {
			return false
		}
	}
	return true
}

func withRuleFacts(d Draft, facts, payload map[string]any) map[string]any {
	out := maps.Clone(facts)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, d.RuleFacts())
	maps.Copy(out, payload)
	return out
}

func absent(names []string, facts map[string]any) []string {
	var out []string
	for _, n := range names {
		if _, ok := facts[n]; !ok {
			out = append(out, n)
		}
	}
	return out
}

// ownFacts is the first of names a FactSpec of d's own kind would name,
// draft.<provider>.<kind>.<field>; "" for none.
func (d Draft) ownFacts(names []string) string {
	prefix := "draft." + string(d.Unit.Provider) + "." + string(d.Kind) + "."
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			return n
		}
	}
	return ""
}

// PayloadFacts are the payload facts of d that spec declares, each the
// string its pointer reaches in d's payload; none when spec is not d's
// kind, and a fact whose pointer reaches no string is absent.
func (d Draft) PayloadFacts(spec KindSpec) map[string]any {
	out := map[string]any{}
	if len(spec.Facts) == 0 || spec.Provider != string(d.Unit.Provider) || spec.Name != string(d.Kind) || len(d.Payload) == 0 {
		return out
	}
	var doc any
	if json.Unmarshal(d.Payload, &doc) != nil {
		return out
	}
	for _, f := range spec.Facts {
		if v, ok := pointAt(doc, f.Pointer).(string); ok {
			out[f.Name] = v
		}
	}
	return out
}

// pointAt is what the JSON pointer ptr (RFC 6901) reaches in doc; nil for
// nothing.
func pointAt(doc any, ptr string) any {
	if ptr == "" {
		return doc
	}
	rest, ok := strings.CutPrefix(ptr, "/")
	if !ok {
		return nil
	}
	for _, tok := range strings.Split(rest, "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch v := doc.(type) {
		case map[string]any:
			doc = v[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) || tok != strconv.Itoa(i) {
				return nil
			}
			doc = v[i]
		default:
			return nil
		}
	}
	return doc
}

// RuleFacts are the facts of d a rule reads: its delivery, as its plugin
// publishes it, its proposer, and its purpose and effects only when a
// workflow or a rule of the loop named them.
func (d Draft) RuleFacts() map[string]any {
	purpose, effects := "", []string{}
	if d.Origin.Kind == OriginAuto {
		purpose = d.Purpose
		effects = append(effects, d.Effects...)
	}
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

// ModeOf is a gate's approval mode as a draft's: send and run send it.
func ModeOf(m gate.Mode) ApprovalMode {
	switch m {
	case gate.ModeSend, gate.ModeRun:
		return Send
	case gate.ModeDelay:
		return Delay
	case gate.ModeDryRun:
		return DryRun
	}
	return Wait
}
