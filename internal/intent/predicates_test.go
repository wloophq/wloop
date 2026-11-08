package intent_test

import (
	"testing"
)

func TestTheBundledReviewerRequiredHoldsForEveryPullRequest(t *testing.T) {
	l, problems := loadProfile(t, "en", nil)
	if len(problems) > 0 {
		t.Fatalf("the bundled profile has problems: %v", problems)
	}
	for _, owner := range []string{"acme", "solo"} {
		if ok, err := l.Predicates.Holds("github", "reviewer_required", map[string]any{"pr.owner": owner}); err != nil || !ok {
			t.Errorf("reviewer_required for %s = %v, %v; want true", owner, ok, err)
		}
	}
}

func TestAUserLayerSetsReviewerRequiredPerOwner(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{
		"plugins/github/predicates.toml": `reviewer_required = "!(pr.owner in ['solo', 'wloophq'])"` + "\n",
	})
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	for owner, want := range map[string]bool{"solo": false, "wloophq": false, "acme": true} {
		if ok, err := l.Predicates.Holds("github", "reviewer_required", map[string]any{"pr.owner": owner}); err != nil || ok != want {
			t.Errorf("reviewer_required for %s = %v, %v; want %v", owner, ok, err, want)
		}
	}
}

func TestAMalformedPredicateIsAProblemAndTheBundledOneStays(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":       `reviewer_required = "pr.owner in ['solo'"`,
		"no such fact": `reviewer_required = "pr.nope == 'solo'"`,
		"not a bool":   `reviewer_required = "pr.owner"`,
		"not a string": `reviewer_required = true`,
	} {
		t.Run(name, func(t *testing.T) {
			l, problems := loadProfile(t, "en", map[string]string{"plugins/github/predicates.toml": body + "\n"})
			if _, ok := problemAt(problems, "reviewer_required", ""); !ok {
				t.Fatalf("problems %v name no reviewer_required", problems)
			}
			if ok, err := l.Predicates.Holds("github", "reviewer_required", map[string]any{"pr.owner": "solo"}); err != nil || !ok {
				t.Errorf("reviewer_required = %v, %v; want the bundled true", ok, err)
			}
		})
	}
}

func BenchmarkReviewerRequiredOverAReportChunk(b *testing.B) {
	l, problems := loadProfile(b, "en", map[string]string{
		"plugins/github/predicates.toml": `reviewer_required = "!(pr.owner in ['solo', 'wloophq'])"` + "\n",
	})
	if len(problems) > 0 {
		b.Fatal(problems)
	}
	sets := make([]map[string]any, 200)
	for i := range sets {
		sets[i] = map[string]any{"pr.owner": "acme", "pr.repo": "acme/api", "pr.author": "ana", "pr.labels": []string{"x"}, "pr.mine": true}
	}
	for b.Loop() {
		for _, s := range sets {
			if _, err := l.Predicates.Holds("github", "reviewer_required", s); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func TestAPredicateTheManifestDoesNotDeclareIsAProblem(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"plugins/github/predicates.toml": `solo = "true"` + "\n"})
	if _, ok := problemAt(problems, "solo", "reviewer_required"); !ok {
		t.Fatalf("problems %v do not refuse solo naming the declared one", problems)
	}
	if _, err := l.Predicates.Holds("github", "solo", nil); err == nil {
		t.Error("Holds answered a predicate the manifest does not declare")
	}
}
