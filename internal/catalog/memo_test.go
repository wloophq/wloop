package catalog

import (
	"testing"

	intentload "github.com/wloophq/wloop/internal/intent"
)

func countBuilds(t *testing.T) *int {
	t.Helper()
	n := new(int)
	onBuild = func() { *n++ }
	t.Cleanup(func() { onBuild = nil })
	return n
}

func TestSpecsBuildsOncePerLoaded(t *testing.T) {
	l := bundled(t)
	Specs(l)
	n := countBuilds(t)
	for range 70 {
		States(Facts{Intents: l})
		Specs(l)
		VersionOf(l)
	}
	if *n != 0 {
		t.Fatalf("specs built %d times on a warm Loaded", *n)
	}
	if VersionOf(l) != Version(buildSpecs(l)) {
		t.Fatal("the memoized version differs from the specs'")
	}
}

func TestANewLoadedRebuildsTheSpecs(t *testing.T) {
	a := bundled(t)
	Specs(a)
	b := &intentload.Loaded{Registry: a.Registry, Templates: a.Templates, Programs: a.Programs}
	n := countBuilds(t)
	Specs(b)
	if *n != 1 {
		t.Fatalf("builds for a new Loaded = %d, want 1", *n)
	}
	Specs(b)
	if *n != 1 {
		t.Fatalf("builds after a repeat = %d, want 1", *n)
	}
}
