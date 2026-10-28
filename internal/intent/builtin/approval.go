package builtin

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
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

// ApprovalRule is the [drafts.approval] rule of a whose when is compiled
// over env. A task.prop.* fact it reads must be typed in env, which only the
// profile does, and one env marks untrusted refuses the rule as an
// untrusted fact of the table does. It names comment delivery when its top-level conjunction holds
// draft.delivery == "comment", the only way a rule releases a comment.
func ApprovalRule(when string, a draft.Approval, env expr.Env) (draft.Rule, error) {
	for _, name := range DynamicFacts(when) {
		if _, typed := env[name]; !typed || !strings.HasPrefix(name, PropPrefix) {
			return draft.Rule{}, &UntrustedError{Fact: name}
		}
	}
	p, err := expr.CompileWorkflow(when, env)
	if err != nil {
		return draft.Rule{}, err
	}
	if workflow.ReadsMarks(p) {
		return draft.Rule{}, ErrRuleMarks
	}
	var subjects []string
	for _, name := range p.Reads() {
		if IsUntrusted(name) || env.Untrusted(name) {
			return draft.Rule{}, &UntrustedError{Fact: name}
		}
		f, ok := workflowFact(name)
		switch {
		case !ok || f.Slot == "" || f.Slot == DraftSlot:
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

// Source is the rule's when as the manifest wrote it.
func (c condition) Source() string {
	if s, ok := c.p.(interface{ Source() string }); ok {
		return s.Source()
	}
	return ""
}

var (
	quotedText  = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	dynamicName = regexp.MustCompile(`(?:^|[^\w.])((?:` + regexp.QuoteMeta(ExtraPrefix) + `|` + regexp.QuoteMeta(ClassifyPrefix) + `|` + regexp.QuoteMeta(PropPrefix) + `)[A-Za-z_]\w*)`)
)

// DynamicFacts are the brief.extra.*, classify.* and task.prop.* facts src
// names outside its string literals: facts the profile declares, which a
// check without the profile cannot type, so it refuses them in a rule that
// releases. A brief's extra field and a classification's output are
// untrusted whatever their name; a property is untrusted when an agent or a
// plugin writes it, which only the profile says.
func DynamicFacts(src string) []string {
	var out []string
	for _, m := range dynamicName.FindAllStringSubmatch(quotedText.ReplaceAllString(src, `""`), -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}
