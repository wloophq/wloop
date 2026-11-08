package catalog

import (
	"strings"
	"testing"
)

func TestDraftStatesFollowTheDraft(t *testing.T) {
	enabled := func(f DraftFacts) map[string]bool {
		out := map[string]bool{}
		for _, st := range DraftStates(bundled(t), f) {
			out[st.ID] = st.Enabled
		}
		return out
	}
	cases := map[string]map[string]bool{
		"held":    {"draft.edit": true, "draft.retry": false, "draft.close-silently": false},
		"failed":  {"draft.edit": true, "draft.retry": true, "draft.close-silently": false},
		"queued":  {"draft.edit": false, "draft.retry": false, "draft.close-silently": false},
		"sending": {"draft.edit": false, "draft.retry": false, "draft.close-silently": false},
		"sent":    {"draft.edit": false, "draft.retry": false, "draft.close-silently": false},
		"dropped": {"draft.edit": false, "draft.retry": false, "draft.close-silently": false},
	}
	for state, want := range cases {
		got := enabled(DraftFacts{State: state})
		if len(got) != len(want) {
			t.Errorf("%s: actions %v, want the delivery's alone", state, got)
		}
		for id, on := range want {
			if got[id] != on {
				t.Errorf("%s: %s enabled %v, want %v", state, id, got[id], on)
			}
		}
	}
	for _, st := range DraftStates(bundled(t), DraftFacts{State: "held", Provider: "slack", Disabled: true}) {
		if st.Enabled || st.Reason != "slack is disabled" {
			t.Errorf("disabled plugin: %+v", st)
		}
	}
	quiet := enabled(DraftFacts{State: "failed", Provider: "slack", NoPublisher: true})
	if quiet["draft.retry"] || !quiet["draft.edit"] {
		t.Errorf("no publisher: %v", quiet)
	}
	for _, st := range DraftStates(bundled(t), DraftFacts{State: "failed", MayHaveBeenPosted: true}) {
		if (st.ID == "draft.retry") != (st.Confirm != "") {
			t.Errorf("confirm on %+v", st)
		}
	}
}

// draftActionState is the state of action id on a draft of f.
func draftActionState(t *testing.T, f DraftFacts, id string) (enabled bool, why string) {
	t.Helper()
	for _, st := range DraftStates(bundled(t), f) {
		if st.ID == id {
			return st.Enabled, st.Reason
		}
	}
	t.Fatalf("no %s among the draft actions", id)
	return false, ""
}

func TestDraftActionsOfferCloseSilentlyOnlyWhenItApplies(t *testing.T) {
	closure := DraftFacts{State: "held", Provider: "slack", Effects: []string{"unit.closed"}, Workflow: "slack-closure"}
	if on, why := draftActionState(t, closure, "draft.close-silently"); !on {
		t.Errorf("a held closure: disabled, %q", why)
	}
	for name, c := range map[string]struct {
		f    DraftFacts
		want string
	}{
		"a nudge":                    {DraftFacts{State: "held", Provider: "slack", Effects: []string{"task.nudged"}}, "posts"},
		"a closure that also nudges": {DraftFacts{State: "held", Provider: "slack", Effects: []string{"unit.closed", "task.nudged"}}, "posts"},
		"a reply":                    {DraftFacts{State: "held", Provider: "slack"}, "applies nothing"},
		"a failed closure":           {DraftFacts{State: "failed", Provider: "slack", Effects: []string{"unit.closed"}}, "failed"},
	} {
		if on, why := draftActionState(t, c.f, "draft.close-silently"); on || !strings.Contains(why, c.want) {
			t.Errorf("%s: enabled %v, why %q, want it to say %q", name, on, why, c.want)
		}
	}
}

func TestDraftActionsOfferSkipWorkflowOnWorkflowNudges(t *testing.T) {
	nudge := DraftFacts{State: "held", Provider: "slack", Workflow: "slack-stalled-nudge", Effects: []string{"task.nudged"}}
	if on, why := draftActionState(t, nudge, "draft.skip-workflow"); !on {
		t.Errorf("a workflow's nudge: disabled, %q", why)
	}
	mine := nudge
	mine.Workflow = ""
	if on, why := draftActionState(t, mine, "draft.skip-workflow"); on || !strings.Contains(why, "no workflow") {
		t.Errorf("a nudge you wrote: %v %q", on, why)
	}
	skipped := nudge
	skipped.Skipped = true
	if on, why := draftActionState(t, skipped, "draft.skip-workflow"); on || !strings.Contains(why, "already") {
		t.Errorf("a skipped conversation: %v %q", on, why)
	}
	closure := DraftFacts{State: "held", Provider: "slack", Workflow: "slack-closure", Effects: []string{"unit.closed"}}
	for _, st := range DraftStates(bundled(t), closure) {
		if st.ID == "draft.skip-workflow" {
			t.Errorf("a closure offers %+v", st)
		}
	}
}
