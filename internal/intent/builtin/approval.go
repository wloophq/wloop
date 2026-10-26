package builtin

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// ErrRuleMarks refuses a rule's when that calls handled().
var ErrRuleMarks = errors.New("a rule reads no marks: handled() is for a workflow's when")

// UntrustedError refuses a rule's when that reads an untrusted fact.
type UntrustedError struct{ Fact string }

func (e *UntrustedError) Error() string {
	return fmt.Sprintf("reads %s, which no draft approval rule may: a third party's text or the proposing turn sets its value", e.Fact)
}

// SubjectsError refuses a rule's when that reads the facts of two subjects,
// which no draft has.
type SubjectsError struct{ Subjects []string }

func (e *SubjectsError) Error() string {
	return fmt.Sprintf("reads facts of %v, but a draft has one subject, so it never applies", e.Subjects)
}

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
	var subjects []string
	for _, name := range p.Reads() {
		f, ok := workflowFact(name)
		switch {
		case !ok || f.Slot == "" || f.Slot == DraftSlot:
		case f.Untrusted:
			return draft.Rule{}, &UntrustedError{Fact: name}
		case !slices.Contains(subjects, f.Slot):
			subjects = append(subjects, f.Slot)
		}
	}
	if len(subjects) > 1 {
		slices.Sort(subjects)
		return draft.Rule{}, &SubjectsError{Subjects: subjects}
	}
	return draft.Rule{When: condition{p}, Approval: a, Subjects: subjects,
		NamesComment: expr.Requires(p, DraftDelivery, string(draft.CommentDelivery))}, nil
}

// NeverReleasesComment says that r releases, reads draft.delivery and pins
// it to no delivery, yet does not name comment delivery: a form such as
// draft.delivery in ["comment"] reads as one that may release a comment,
// and never does.
func NeverReleasesComment(r draft.Rule) bool {
	c, ok := r.When.(condition)
	if !ok || !r.Approval.Releases() || r.NamesComment || !slices.Contains(c.p.Reads(), DraftDelivery) {
		return false
	}
	return !expr.Requires(c.p, DraftDelivery, string(draft.Message)) && !expr.Requires(c.p, DraftDelivery, string(draft.Reaction))
}

// CommentForm is how a rule names comment delivery.
const CommentForm = `draft.delivery == "comment" as a term of the top-level &&`

func workflowFact(name string) (Fact, bool) {
	for _, f := range Facts {
		if f.Name == name && (slices.Contains(f.Slots, WorkflowSlot) || slices.Contains(f.Slots, ApprovalSlot)) {
			return f, true
		}
	}
	return Fact{}, false
}

type condition struct{ p workflow.Predicate }

func (c condition) Holds(facts map[string]any, now time.Time) (bool, error) {
	return c.p.Match(workflow.Vars{Facts: facts, Now: now})
}
