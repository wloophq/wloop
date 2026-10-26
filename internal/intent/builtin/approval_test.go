package builtin

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
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
	for _, name := range []string{"draft.delivery", "draft.proposer", "draft.effects", "pr.author", "pr.repo"} {
		if _, ok := env[name]; !ok {
			t.Errorf("%s is missing", name)
		}
	}
	for _, name := range []string{"draft.purpose", "draft.items_held", "brief.risk", "slack.conversation", "trigger.kind", "config.cleanup.idle"} {
		if _, ok := env[name]; ok {
			t.Errorf("%s is readable", name)
		}
	}
	if _, ok := ApprovalEnv(true, "slack")[DraftPurpose]; !ok {
		t.Error("a workflow's rule does not read draft.purpose")
	}
}

func TestAnApprovalRuleNamesCommentDeliveryByItsConjunction(t *testing.T) {
	env := ApprovalEnv(false, "pr")
	none := draft.Approval{Mode: draft.None}
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
