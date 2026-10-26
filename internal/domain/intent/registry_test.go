package intent

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func fixture() *Registry {
	return NewRegistry([]Def{
		{Name: "review", UserFacing: true, Answerable: true, Session: Session{Title: "review"}, Reconcile: "my_review",
			Inputs: []Input{{Name: "dry", Type: "flag", Flag: "--dry"}},
			Record: RecordRules{States: []string{"done", "asked"}, HistoryArtifact: "review-draft"}},
		{Name: "probe", Session: Session{Title: "probe", CloseWhenDone: true}, Record: RecordRules{States: []string{"done"}}},
		{Name: "nudge", UserFacing: true, Session: Session{Title: "nudge", CloseWhenDone: true}},
		{Name: "address", UserFacing: true, Answerable: true, Session: Session{Title: "triage"},
			Inputs: []Input{{Name: "self_review", Type: "flag", Flag: "--self-review"}, {Name: "take_over", Type: "flag", Flag: "--take-over"}}},
		{Name: "ask-author", UserFacing: true, Answerable: true, Session: Session{Title: "triage"},
			Inputs: []Input{{Name: "take_over", Type: "flag", Flag: "--take-over"}}},
	})
}

func names(defs []Def) []Name {
	var out []Name
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func TestRegistryKeepsNameOrderAndLooksUp(t *testing.T) {
	r := fixture()
	if got, want := names(r.All()), []Name{"address", "ask-author", "nudge", "probe", "review"}; !slices.Equal(got, want) {
		t.Errorf("All() = %v, want %v", got, want)
	}
	d, ok := r.Get("nudge")
	if !ok || d.Name != "nudge" || d.Session.Title != "nudge" {
		t.Errorf("Get(nudge) = %+v, %v", d, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Error("Get(missing) found a definition")
	}
	all := r.All()
	all[0].Name = "changed"
	if r.All()[0].Name != "address" {
		t.Error("All() exposes the registry's slice")
	}
}

func TestAnswerableAndUserFacingFilter(t *testing.T) {
	r := fixture()
	if got, want := r.Answerable(), []string{"address", "ask-author", "review"}; !slices.Equal(got, want) {
		t.Errorf("Answerable() = %v, want %v", got, want)
	}
	if got, want := names(r.UserFacing()), []Name{"address", "ask-author", "nudge", "review"}; !slices.Equal(got, want) {
		t.Errorf("UserFacing() = %v, want %v", got, want)
	}
}

func TestNamesReadsAsAList(t *testing.T) {
	for _, c := range []struct {
		defs []Def
		want string
	}{
		{nil, ""},
		{[]Def{{Name: "review", UserFacing: true}}, "review"},
		{[]Def{{Name: "review", UserFacing: true}, {Name: "address", UserFacing: true}, {Name: "probe"}}, "address or review"},
		{fixture().All(), "address, ask-author, nudge or review"},
	} {
		if got := NewRegistry(c.defs).Names(); got != c.want {
			t.Errorf("Names() = %q, want %q", got, c.want)
		}
	}
}

func TestDeclaringFlag(t *testing.T) {
	r := fixture()
	if got, want := r.DeclaringFlag("--take-over"), []Name{"address", "ask-author"}; !slices.Equal(got, want) {
		t.Errorf("DeclaringFlag(--take-over) = %v, want %v", got, want)
	}
	if got := r.DeclaringFlag("--nope"); len(got) != 0 {
		t.Errorf("DeclaringFlag(--nope) = %v", got)
	}
}

func TestRulesLooksUpByName(t *testing.T) {
	r := fixture()
	rules, err := r.Rules("review")
	if err != nil || rules.HistoryArtifact != "review-draft" || !slices.Equal(rules.States, []string{"done", "asked"}) {
		t.Errorf("Rules(review) = %+v, %v", rules, err)
	}
	if _, err := r.Rules("missing"); err == nil || err.Error() != `the intent "missing" is not one `+brand.Name+` knows` {
		t.Errorf("Rules(missing): %v", err)
	}
}

func TestClosesWhenDoneReconcileAndSessionTitles(t *testing.T) {
	r := fixture()
	for intent, want := range map[string]bool{"probe": true, "nudge": true, "review": false, "missing": false} {
		if got := r.ClosesWhenDone(intent); got != want {
			t.Errorf("ClosesWhenDone(%s) = %v", intent, got)
		}
	}
	if r.Reconcile("review") != "my_review" || r.Reconcile("address") != "" || r.Reconcile("missing") != "" {
		t.Error("Reconcile reads the wrong definition")
	}
	if got, want := r.SessionTitles(), []string{"triage", "nudge", "review"}; !slices.Equal(got, want) {
		t.Errorf("SessionTitles() = %v, want %v", got, want)
	}
}

func TestForProviderListsTheProvidersIntentsAndAnyOnes(t *testing.T) {
	r := NewRegistry([]Def{{Name: "review", Provider: unit.GitHub}, {Name: "discuss", Provider: "thread"}, {Name: "act", Provider: AnyProvider}})
	names := func(p unit.ProviderID) []Name {
		var out []Name
		for _, d := range r.ForProvider(p) {
			out = append(out, d.Name)
		}
		return out
	}
	if got := names(unit.GitHub); !slices.Equal(got, []Name{"act", "review"}) {
		t.Errorf("github: %v", got)
	}
	if got := names("thread"); !slices.Equal(got, []Name{"act", "discuss"}) {
		t.Errorf("thread: %v", got)
	}
}
