package builtin

import (
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// ErrRuleMarks refuses a rule's when that calls handled().
var ErrRuleMarks = errors.New("a rule reads no marks: handled() is for a workflow's when")

// ApprovalRule is the [draft_approval] rule of a whose when is compiled
// over env. It names comment delivery when its top-level conjunction holds
// draft.delivery == "comment", the only way a rule releases a comment.
func ApprovalRule(when string, a draft.Approval, env expr.Env) (draft.Rule, error) {
	p, err := expr.CompileWorkflow(when, env)
	if err != nil {
		return draft.Rule{}, err
	}
	if workflow.ReadsMarks(p) {
		return draft.Rule{}, ErrRuleMarks
	}
	return draft.Rule{When: condition{p}, Approval: a, NamesComment: expr.Requires(p, DraftDelivery, string(draft.CommentDelivery))}, nil
}

type condition struct{ p workflow.Predicate }

func (c condition) Holds(facts map[string]any, now time.Time) (bool, error) {
	return c.p.Match(workflow.Vars{Facts: facts, Now: now})
}
