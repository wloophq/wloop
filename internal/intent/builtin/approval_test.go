package builtin

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/expr"
)

func TestTheApprovalFactsAreTheDraftsRuleFacts(t *testing.T) {
	var declared []string
	for _, f := range Facts {
		if slices.Contains(f.Slots, ApprovalSlot) {
			declared = append(declared, f.Name)
		}
	}
	slices.Sort(declared)
	if got := slices.Sorted(maps.Keys(draft.Draft{}.RuleFacts())); !slices.Equal(got, declared) {
		t.Fatalf("draft.RuleFacts gives %v, the facts table declares %v", got, declared)
	}
}

func TestAnApprovalEnvReadsItsSlotsAndNeverABrief(t *testing.T) {
	env := ApprovalEnv(false, "pr")
	for _, name := range []string{"draft.delivery", "draft.proposer", "pr.author", "pr.repo"} {
		if _, ok := env[name]; !ok {
			t.Errorf("%s is missing", name)
		}
	}
	for _, name := range []string{"draft.purpose", "draft.effects", "draft.items_held", "brief.risk", "slack.conversation", "trigger.kind", "config.cleanup.idle"} {
		if _, ok := env[name]; ok {
			t.Errorf("%s is readable", name)
		}
	}
	for _, name := range AutoOnly {
		if _, ok := ApprovalEnv(true, "slack")[name]; !ok {
			t.Errorf("a workflow's rule does not read %s", name)
		}
	}
}

func TestAnApprovalRuleNamesCommentDeliveryByItsConjunction(t *testing.T) {
	env := ApprovalEnv(false, "pr")
	none := draft.Approval{Mode: draft.Send}
	for when, want := range map[string]bool{
		`draft.delivery == "comment" && pr.author == "dependabot[bot]"`: true,
		`pr.repo == "acme/docs"`:                                false,
		`draft.delivery == "comment" || pr.repo == "acme/docs"`: false,
	} {
		r, err := ApprovalRule(when, none, env)
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		if r.NamesComment != want || r.Approval != none {
			t.Errorf("%s: %+v", when, r)
		}
	}
	r, _ := ApprovalRule(`pr.author == "dependabot[bot]"`, none, env)
	if ok, err := r.When.Holds(map[string]any{"pr.author": "dependabot[bot]"}, time.Time{}); !ok || err != nil {
		t.Errorf("holds %v %v", ok, err)
	}
	if _, err := ApprovalRule(`handled("x", pr.author)`, none, env); !errors.Is(err, ErrRuleMarks) {
		t.Errorf("handled(): %v", err)
	}
	if _, err := ApprovalRule(`draft.purpose == "thanks"`, none, env); err == nil {
		t.Error("an intent's rule reads draft.purpose")
	}
}

func TestAnApprovalRuleNamesTheSubjectsItReads(t *testing.T) {
	env := ApprovalEnv(true, UnitSlots()...)
	none := draft.Approval{Mode: draft.Send}
	for when, want := range map[string][]string{
		`pr.author == "dependabot[bot]" && draft.delivery == "comment"`: {"pr"},
		`slack.conversation == "dm"`:                                    {"slack"},
		`draft.proposer == "workflow:slack-thanks"`:                     nil,
	} {
		r, err := ApprovalRule(when, none, env)
		if err != nil || !slices.Equal(r.Subjects, want) {
			t.Errorf("%s: %v %v, want %v", when, r.Subjects, err, want)
		}
	}
	var se *SubjectsError
	if _, err := ApprovalRule(`pr.author == "x" && slack.conversation == "dm"`, none, env); !errors.As(err, &se) || !slices.Equal(se.Subjects, []string{"pr", "slack"}) {
		t.Errorf("two subjects: %v", err)
	}
}

func TestARuleReadingAnUntrustedFactIsRefused(t *testing.T) {
	env := ApprovalEnv(true, UnitSlots()...)
	var n int
	for _, f := range Facts {
		if !f.Untrusted {
			continue
		}
		n++
		if _, ok := env[f.Name]; !ok {
			t.Errorf("%s is untrusted but no rule's env holds it", f.Name)
			continue
		}
		var ue *UntrustedError
		if _, err := ApprovalRule(f.Name+" == "+zeroLiteral(f), draft.Approval{Mode: draft.Send}, env); !errors.As(err, &ue) || ue.Fact != f.Name {
			t.Errorf("%s: %v", f.Name, err)
		}
	}
	for _, name := range []string{"slack.started_by_me", "slack.closure_posted", "entry.unit_started_by_me", "entry.unit_closure_posted",
		"slack.items_open", "slack.items_unowned", "slack.items_unowned_age_days", "entry.items_open", "entry.items_resolved", "entry.items_held"} {
		if i := slices.IndexFunc(Facts, func(f Fact) bool { return f.Name == name }); i < 0 || !Facts[i].Untrusted {
			t.Errorf("%s is trusted", name)
		}
	}
	if n == 0 {
		t.Fatal("no fact is untrusted")
	}
}

func zeroLiteral(f Fact) string {
	switch f.Type {
	case expr.Bool:
		return "true"
	case expr.Int:
		return "0"
	case expr.List:
		return "[]"
	case expr.Time:
		return `timestamp("2026-01-01T00:00:00Z")`
	case expr.Duration:
		return `duration("1h")`
	}
	return `""`
}

func TestNeverReleasesCommentFlagsAReleasingRuleThatReadsDeliveryAndPinsNone(t *testing.T) {
	env := ApprovalEnv(false, "pr")
	for when, want := range map[string]bool{
		`draft.delivery in ["comment"]`:                         true,
		`draft.delivery != "reaction"`:                          true,
		`draft.delivery == "comment" && pr.author == "x"`:       false,
		`draft.delivery == "message" && pr.repo == "acme/docs"`: false,
		`draft.delivery == "reaction"`:                          false,
		`pr.author == "x"`:                                      false,
	} {
		r, err := ApprovalRule(when, draft.Approval{Mode: draft.Send}, env)
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		if got := NeverReleasesComment(r); got != want {
			t.Errorf("%s: %v", when, got)
		}
		r.Approval = draft.Approval{Mode: draft.Wait}
		if NeverReleasesComment(r) {
			t.Errorf("%s holding: flagged", when)
		}
	}
}
