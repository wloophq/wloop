package builtin

import (
	"slices"
	"strings"
	"testing"
)

// removedKeys are the keys a release renamed or removed. None may come
// back as a row: a user file that still sets one must fail as unknown, not
// load with another meaning.
var removedKeys = map[Surface][]string{
	SurfaceIntent: {"key", "modes", "round_kind", "cap", "reason", "reconcile", "prepare", "context", "launch", "policy",
		"drives_row", "board", "on_sent", "draft_effects", "draft_approval", "reason_fact", "refuse_when", "records", "also_profiles",
		"headline.new.text", "action.visible", "action.enabled", "action.reason_off", "action.hint", "action.note_when_question",
		"resume.head_moved"},
	SurfaceWorkflow: {"description", "subject", "mode", "trigger.every", "approval.default", "approval.wait_when", "allow_visible",
		"draft_approval", "withdraw"},
	SurfaceConfig: {"workflows.*.mode", "workflows.*.approval.default", "workflows.*.draft_approval", "project_settings", "proposals",
		"rounds", "turns.automated_per_day", "classify.daily_cap", "brief.max_diff_bytes", "slack.extraction.max_call_usd",
		"slack.extraction.max_day_usd", "slack.extraction.max_day_calls", "plugins", "automated_reviewers", "owners",
		"trusted_authors", "review_prefix", "followups_without_workspace", "address_without_workspace", "projects.trusted_authors",
		"projects.followups_without_workspace", "projects.address_without_workspace"},
}

var removedStepArgs = map[string][]string{
	StepTurnStart:    {"round", "mode"},
	StepDraftPropose: {"on_posted", "hold", "hold_after_sent"},
}

var removedFacts = []string{"row.adopted", "row.held", "row.muted", "row.nudge.who", "row.nudge.why", "row.nudge.refusal",
	"pr.nudge_refusal", "last_review.exists", "last_review.verdict", "last_review.decision", "turn.round"}

func TestNoRemovedKeyReturns(t *testing.T) {
	t.Parallel()
	for surface, keys := range removedKeys {
		for _, k := range Grammar {
			if k.In == surface && slices.Contains(keys, bare(k.Path)) {
				t.Errorf("%s %s was removed and is a row again", surface, k.Path)
			}
		}
	}
	for step, args := range removedStepArgs {
		for _, a := range StepArgs(step) {
			if slices.Contains(args, a) {
				t.Errorf("%s's %s was removed and is an argument again", step, a)
			}
		}
	}
	for _, f := range Facts {
		if slices.Contains(removedFacts, f.Name) {
			t.Errorf("the fact %s was removed and is a fact again", f.Name)
		}
	}
	for _, v := range DraftModes() {
		if strings.EqualFold(v.Name, "record") {
			t.Error("the draft mode record was removed and is a mode again")
		}
	}
}
