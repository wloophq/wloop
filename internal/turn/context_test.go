package turn

import (
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/instructions"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func pr() *PR {
	return &PR{URL: "https://github.com/acme/api/pull/7", Owner: "acme", Repo: "api", Number: 7}
}

func withPrior(p *PR, verdict string) *PR {
	p.Prior = &PriorReview{Verdict: verdict, Head: "abc"}
	return p
}

func bundled(t *testing.T) *intentload.Loaded {
	t.Helper()
	l, err := Bundled()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func completed(t *testing.T, c Context) Context {
	t.Helper()
	got, err := Complete(c, bundled(t))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// render completes c against the bundled intents and renders it.
func render(c Context) (Rendered, error) {
	l, err := Bundled()
	if err != nil {
		return Rendered{}, err
	}
	if c, err = Complete(c, l); err != nil {
		return Rendered{}, err
	}
	return Render(c, l, "")
}

func TestHeadlineNamesIntentRoundAndTrigger(t *testing.T) {
	for _, c := range []struct {
		ctx  Context
		want string
	}{
		{Context{Intent: Intent(dintent.Review), Payload: pr()}, "First review of acme/api#7, asked for by you."},
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Payload: pr()}, "First review of acme/api#7, started by " + brand.Name + "."},
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Payload: withPrior(pr(), "CHANGES_REQUESTED")}, "Follow-up review of acme/api#7, started by " + brand.Name + "."},
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Planned: PlannedFollowUp, Payload: pr()}, "Follow-up review of acme/api#7, started by " + brand.Name + "."},
		{Context{Intent: Intent(dintent.Review), Payload: withPrior(pr(), "APPROVED"), Plain: true}, "Dry-run review of acme/api#7: trying a profile, nothing is published."},
		{Context{Intent: Intent(dintent.Address), Payload: pr(), Owned: true}, "Address round on acme/api#7, asked for by you."},
		{Context{Intent: Intent(dintent.Address), Trigger: Automated, Payload: pr(), Owned: true}, "Address round on acme/api#7, started by " + brand.Name + "."},
		{Context{Intent: Intent(dintent.Address), Payload: pr(), Inputs: map[string]any{"take_over": true}}, "Take over acme/api#7 and address it, asked for by you."},
		{Context{Intent: Intent(dintent.Address), Payload: pr()}, "Address round on acme/api#7, which someone else opened: ask before taking it over."},
		{Context{Intent: Intent(dintent.Nudge), Payload: pr()}, "Nudge on acme/api#7, asked for by you."},
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Payload: withPrior(pr(), "APPROVED"), Resume: &instructions.Resume{Answer: "go"}}, "Continue the review of acme/api#7: the user answered."},
		{Context{Intent: Intent(dintent.Address), Payload: pr(), Owned: true, Resume: &instructions.Resume{}}, "Continue the address round on acme/api#7 where the last session stopped."},
		{Context{Intent: Intent(dintent.Nudge), Payload: pr(), Resume: &instructions.Resume{Answer: "go"}}, "Continue the nudge on acme/api#7: the user answered."},
		{Context{Intent: Intent(dintent.Probe), Trigger: Automated, Payload: pr()}, brand.Name + " probe acme/api#7, started by " + brand.Name + "."},
		{Context{Intent: Intent(dintent.Probe), Trigger: Automated, Payload: pr(), Resume: &instructions.Resume{}}, "Continue the " + brand.Name + " probe acme/api#7 where the last session stopped."},
	} {
		if got := completed(t, c.ctx).Headline; got != c.want {
			t.Errorf("%+v: %q, want %q", c.ctx, got, c.want)
		}
	}
}

func TestADryRunIsNeverAFollowUp(t *testing.T) {
	c := completed(t, Context{Intent: Intent(dintent.Review), Trigger: Automated, Payload: withPrior(pr(), "APPROVED"), Plain: true})
	if c.Round != "first" || c.FollowUpApproval || c.Records {
		t.Fatalf("a dry run reviews as a first round and records nothing: %q %v %v", c.Round, c.FollowUpApproval, c.Records)
	}
}

func TestOnlyIntentsWithTheRoundDimensionHaveARound(t *testing.T) {
	for _, i := range []Intent{Intent(dintent.Address), Intent(dintent.Nudge), Intent(dintent.Probe)} {
		if c := completed(t, Context{Intent: i, Trigger: Automated, Planned: PlannedFollowUp, Payload: withPrior(pr(), "APPROVED"), Owned: true}); c.Round != "" || c.FollowUpApproval {
			t.Errorf("%s: round %q, follow-up approval %v", i, c.Round, c.FollowUpApproval)
		}
	}
}

func TestTheFollowUpApprovalRuleIsTheSweepFollowUps(t *testing.T) {
	for _, c := range []struct {
		ctx  Context
		want bool
	}{
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Planned: PlannedFollowUp, Payload: pr()}, true},
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Payload: withPrior(pr(), "APPROVED")}, true},
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Planned: PlannedFirst, Payload: withPrior(pr(), "APPROVED")}, false},
		{Context{Intent: Intent(dintent.Review), Payload: withPrior(pr(), "APPROVED")}, false},
		{Context{Intent: Intent(dintent.Review), Trigger: Automated, Payload: pr()}, false},
	} {
		if got := completed(t, c.ctx).FollowUpApproval; got != c.want {
			t.Errorf("%+v: %v, want %v", c.ctx, got, c.want)
		}
	}
}

func TestCompleteIsIdempotent(t *testing.T) {
	for _, s := range Scenarios() {
		if again := completed(t, s.Context); again.Headline != s.Context.Headline || again.Records != s.Context.Records || again.NeedsTakeOver != s.Context.NeedsTakeOver {
			t.Errorf("%s: completing twice changed the turn", s.Name)
		}
	}
}

func TestCompleteRefusesAnUnknownIntent(t *testing.T) {
	if _, err := Complete(Context{Intent: "adopt", Payload: pr()}, bundled(t)); err == nil {
		t.Fatal("adopt is not an intent")
	}
}
