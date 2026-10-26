package intent

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestByRoleTakesTheUsersIntentOverTheBundledOne(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]Def{
		{Name: "review", Role: "round.review", Bundled: true, Provider: unit.GitHub},
		{Name: "my-review", Role: "round.review", Provider: unit.GitHub},
		{Name: "act", Role: "headless.default", Provider: AnyProvider, Bundled: true},
	})
	if d, ok := r.ByRole("round.review", unit.GitHub); !ok || d.Name != "my-review" {
		t.Fatalf("%v %v", d.Name, ok)
	}
	if d, ok := r.ByRole("headless.default", "slack"); !ok || d.Name != "act" {
		t.Fatalf("%v %v", d.Name, ok)
	}
	if _, ok := r.ByRole("round.review", "slack"); ok {
		t.Fatal("a github role served slack")
	}
	if _, ok := r.ByRole("", unit.GitHub); ok {
		t.Fatal("the empty role matched")
	}
}

func TestAListOfProvidersServesEachOfThem(t *testing.T) {
	t.Parallel()
	d := Def{Name: "brief", Builds: "brief", Provider: unit.GitHub, Providers: []unit.ProviderID{unit.GitHub, "slack"}}
	if !d.Serves(unit.GitHub) || !d.Serves("slack") || d.Serves(unit.Session) {
		t.Fatal(d.Providers)
	}
	if b, ok := NewRegistry([]Def{d}).Builder("brief", "slack"); !ok || b.Name != "brief" {
		t.Fatal(b.Name, ok)
	}
}
