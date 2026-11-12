package archtest

import (
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestAllowListRuleShippedFlagsADenyList(t *testing.T) {
	t.Parallel()
	rules := `[[gates.approval.rule]]
when = 'draft.delivery == "review" && draft.github.review.event in ["COMMENT", "REQUEST_CHANGES"]'
mode = "send"
[[gates.approval.rule]]
when = 'draft.github.review.event != "APPROVE"'
mode = "wait"
[[gates.approval.rule]]
when = 'draft.delivery == "review" && draft.github.review.event != "APPROVE"'
mode = "send"
[[gates.approval.rule]]
when = 'draft.delivery == "review" && draft.github.review.event in ["", "COMMENT"]'
mode = "delay"
window = "5m"
`
	fsys := fstest.MapFS{
		"github/profile/intents/review/intent.toml": {Data: []byte(rules)},
		"github/profile/intents/review/notes.toml":  {Data: []byte(rules)},
	}
	got, err := denyListRules(fsys, "planted")
	if err != nil {
		t.Fatal(err)
	}
	flagged(t, "planted", got, 2, "other than as an allow-list")
}

func TestAllowListRuleShippedFlagsAReviewSentWhateverItsEvent(t *testing.T) {
	t.Parallel()
	env := builtin.ApprovalEnv(false, builtin.UnitSlots()...)
	var rules []draft.Rule
	for _, when := range []string{`draft.delivery == "review"`, `draft.delivery == "reply"`, `draft.delivery == "resolve"`} {
		r, err := builtin.ApprovalRule(when, draft.Approval{Mode: draft.Send}, env)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, r)
	}
	def := dintent.Def{Name: "planted", Provider: "github", DraftApproval: &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: rules}}
	flagged(t, "planted", shippedDecisions(def, shippedSends), 3, "want false")
	flagged(t, "no approval", shippedDecisions(dintent.Def{Name: "planted", Provider: "github"}, shippedSends), 1, "no draft approval")
}
