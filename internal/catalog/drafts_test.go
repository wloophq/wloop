package catalog

import (
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
		"held":    {"draft.edit": true, "draft.retry": false},
		"failed":  {"draft.edit": true, "draft.retry": true},
		"queued":  {"draft.edit": false, "draft.retry": false},
		"sending": {"draft.edit": false, "draft.retry": false},
		"sent":    {"draft.edit": false, "draft.retry": false},
		"dropped": {"draft.edit": false, "draft.retry": false},
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
