package catalog

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func enabledDelegates(t *testing.T, f Facts) []string {
	t.Helper()
	specs := Specs(f.Intents)
	var out []string
	for _, st := range States(f) {
		if s, ok := specByID(specs, st.ID); ok && s.Verb == "delegate" && st.Enabled {
			out = append(out, st.ID)
		}
	}
	return out
}

func TestActSpecIsItemScopedWithNoKey(t *testing.T) {
	s, ok := specByID(Specs(bundled(t)), string(dintent.Act))
	if !ok || s.Scope != ScopeItem || s.Key != "" || s.Verb != "delegate" || s.Plugin != "" {
		t.Fatalf("act spec %+v", s)
	}
}

func actState(t *testing.T, f Facts) api.ActionState {
	t.Helper()
	for _, st := range States(f) {
		if st.ID == string(dintent.Act) {
			return st
		}
	}
	t.Fatalf("act is not offered on %v", f.Row.Unit)
	return api.ActionState{}
}

func TestActOfferedWhateverTheRecommendedMode(t *testing.T) {
	l := bundled(t)
	thread := unit.Unit{Provider: "thread", Key: "1"}
	for _, mode := range []string{"", "session", "headless"} {
		mine := Facts{Intents: l, Ref: r7, Row: api.Row{Unit: r7.Unit()}, Mine: true, Open: true, Tracked: true, NudgeWho: []string{}, Recommended: mode}
		got := enabledDelegates(t, mine)
		if !slices.Contains(got, string(dintent.Act)) || !slices.Contains(got, string(dintent.Address)) {
			t.Errorf("recommended %q: delegates %v, want act beside address", mode, got)
		}
		st := actState(t, Facts{Intents: l, Open: true, Row: api.Row{Unit: thread}, NudgeWho: []string{}, Recommended: mode})
		if !st.Enabled || st.Primary != (mode == "headless") {
			t.Errorf("recommended %q: act on a thread %+v, want enabled and primary only when headless is recommended", mode, st)
		}
		if mode != "" && !strings.Contains(st.Note, "recommended: "+mode) {
			t.Errorf("recommended %q: hint %q", mode, st.Note)
		}
	}
}

func TestActOnAPullRequestNeedsItToBeYours(t *testing.T) {
	l := bundled(t)
	for name, c := range map[string]struct {
		f    Facts
		want bool
	}{
		"someone else's": {Facts{Ref: r7, Row: api.Row{Unit: r7.Unit()}}, false},
		"mine":           {Facts{Ref: r7, Row: api.Row{Unit: r7.Unit()}, Mine: true}, true},
		"taken over":     {Facts{Ref: r7, Row: api.Row{Unit: r7.Unit()}, Override: api.Override{Adopted: true}}, true},
		"a thread":       {Facts{Row: api.Row{Unit: unit.Unit{Provider: "thread", Key: "1"}}}, true},
	} {
		c.f.Intents, c.f.Open, c.f.Tracked, c.f.NudgeWho = l, true, true, []string{}
		st := actState(t, c.f)
		if st.Enabled != c.want || (!c.want && st.Reason != "Not yours: take it over first") {
			t.Errorf("%s: %+v", name, st)
		}
	}
}

// The bundled act must never take a GitHub row's delegate from review or address: it comes after them in
// the row's order, and it is the provider-any action of its origin while they are github's.
func TestGitHubDelegateStaysWithReviewAndAddress(t *testing.T) {
	l := bundled(t)
	specs := Specs(l)
	act, _ := specByID(specs, string(dintent.Act))
	for _, mine := range []bool{true} {
		f := Facts{Intents: l, Ref: r7, Row: api.Row{Unit: r7.Unit()}, Open: true, Tracked: true, Mine: mine, NudgeWho: []string{}}
		got := enabledDelegates(t, f)
		if len(got) < 2 || got[len(got)-1] != string(dintent.Act) {
			t.Fatalf("mine %v: delegates %v, want review or address before act", mine, got)
		}
		for _, id := range got[:len(got)-1] {
			s, _ := specByID(specs, id)
			if s.Plugin != string(unit.GitHub) || filepath.IsAbs(s.Source) {
				t.Errorf("%s: plugin %q source %q, want a bundled github intent", id, s.Plugin, s.Source)
			}
		}
	}
	if act.Plugin != "" || filepath.IsAbs(act.Source) {
		t.Errorf("act plugin %q source %q, want a bundled provider-any intent", act.Plugin, act.Source)
	}
}
