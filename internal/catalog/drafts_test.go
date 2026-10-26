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
		"proposed":  {"draft.approve": true, "draft.edit": true, "draft.discard": true, "draft.retry": false},
		"failed":    {"draft.approve": false, "draft.edit": true, "draft.discard": true, "draft.retry": true},
		"approved":  {"draft.approve": false, "draft.edit": false, "draft.discard": true, "draft.retry": false},
		"sending":   {"draft.approve": false, "draft.edit": false, "draft.discard": false, "draft.retry": false},
		"sent":      {"draft.approve": false, "draft.edit": false, "draft.discard": false, "draft.retry": false},
		"discarded": {"draft.approve": false, "draft.edit": false, "draft.discard": false, "draft.retry": false},
	}
	for state, want := range cases {
		got := enabled(DraftFacts{State: state})
		for id, on := range want {
			if got[id] != on {
				t.Errorf("%s: %s enabled %v, want %v", state, id, got[id], on)
			}
		}
	}
	for _, st := range DraftStates(bundled(t), DraftFacts{State: "proposed", Provider: "slack", Disabled: true}) {
		if st.ID == "draft.discard" {
			if !st.Enabled {
				t.Errorf("discard of a disabled plugin's draft: %+v", st)
			}
		} else if st.Enabled || st.Reason != "slack is disabled" {
			t.Errorf("disabled plugin: %+v", st)
		}
	}
	quiet := enabled(DraftFacts{State: "failed", Provider: "slack", NoPublisher: true})
	if quiet["draft.retry"] || !quiet["draft.edit"] || !quiet["draft.discard"] {
		t.Errorf("no publisher: %v", quiet)
	}
	for _, st := range DraftStates(bundled(t), DraftFacts{State: "failed", MayHaveBeenPosted: true}) {
		if (st.ID == "draft.retry") != (st.Confirm != "") {
			t.Errorf("confirm on %+v", st)
		}
	}
}
