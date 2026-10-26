package intent

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// mDraftApproval is the [draft_approval] table intents and workflows share.
type mDraftApproval struct {
	Mode   *string         `toml:"mode"`
	Window *string         `toml:"window"`
	Rule   []mApprovalRule `toml:"rule"`
}

type mApprovalRule struct {
	When   *string `toml:"when"`
	Mode   *string `toml:"mode"`
	Window *string `toml:"window"`
}

var draftApprovalModes = []string{string(draft.Wait), string(draft.Delay), string(draft.Send), string(draft.DryRun)}

// draftApproval is the policy of m, its rules' when compiled over env; nil
// when m is. A rule that releases while reading draft.delivery in a form
// that never names comment delivery loads with a warning.
func (s *sheet) draftApproval(m *mDraftApproval, env expr.Env) *draft.Policy {
	if m == nil {
		return nil
	}
	a, ok := s.approval("draft_approval", m.Mode, m.Window)
	if !ok {
		return nil
	}
	p := &draft.Policy{Default: a}
	for i, r := range m.Rule {
		path := fmt.Sprintf("draft_approval.rule[%d]", i)
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

// approval is the mode and window at path.
func (s *sheet) approval(path string, mode, window *string) (draft.Approval, bool) {
	if mode == nil {
		s.add(path+".mode", "is required: one of %s", quoted(draftApprovalModes))
		return draft.Approval{}, false
	}
	a := draft.Approval{Mode: draft.ApprovalMode(*mode)}
	switch {
	case !slices.Contains(draftApprovalModes, *mode):
		s.oneOf(path+".mode", *mode, draftApprovalModes)
		return draft.Approval{}, false
	case a.Mode == draft.Delay && window == nil:
		s.add(path+".window", "is required with mode delay: how long the user has before the draft is sent, such as 10m")
		return draft.Approval{}, false
	case a.Mode != draft.Delay && window != nil:
		s.add(path+".window", "is read only with mode delay, not with %s", a.Mode)
		return draft.Approval{}, false
	case window != nil:
		a.Window = s.duration(path+".window", *window)
		if a.Validate() != nil {
			s.add(path+".window", "must be longer than zero")
			return draft.Approval{}, false
		}
	}
	return a, true
}
