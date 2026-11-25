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

func TestTheBundledReviewWantedIsARequestOfMyReviewByName(t *testing.T) {
	l, problems := loadProfile(t, "en", nil)
	if len(problems) > 0 {
		t.Fatalf("the bundled profile has problems: %v", problems)
	}
	for _, requested := range []bool{true, false} {
		facts := map[string]any{"pr.review_requested_me": requested, "pr.mine": false, "pr.repo": "acme/api"}
		if ok, err := l.Predicates.Holds("github", "review_wanted", facts); err != nil || ok != requested {
			t.Errorf("review_wanted with pr.review_requested_me %v = %v, %v; want %v", requested, ok, err, requested)
		}
	}
}

func TestAUserLayerWantsMyReviewOnEveryPullRequestOfARepository(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{
		"plugins/github/predicates.toml": `review_wanted = "pr.repo == 'acme/api' && !pr.mine"` + "\n",
	})
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	for _, c := range []struct {
		repo string
		mine bool
		want bool
	}{{"acme/api", false, true}, {"acme/api", true, false}, {"acme/web", false, false}} {
		facts := map[string]any{"pr.review_requested_me": false, "pr.mine": c.mine, "pr.repo": c.repo}
		if ok, err := l.Predicates.Holds("github", "review_wanted", facts); err != nil || ok != c.want {
			t.Errorf("review_wanted for %s mine %v = %v, %v; want %v", c.repo, c.mine, ok, err, c.want)
		}
	}
}

func TestAUserLayerSetsReviewerRequiredPerOwner(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{
		"plugins/github/predicates.toml": `reviewer_required = "!(pr.owner in ['solo', 'lone'])"` + "\n",
	})
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	for owner, want := range map[string]bool{"solo": false, "lone": false, "acme": true} {
		if ok, err := l.Predicates.Holds("github", "reviewer_required", map[string]any{"pr.owner": owner}); err != nil || ok != want {
			t.Errorf("reviewer_required for %s = %v, %v; want %v", owner, ok, err, want)
		}
	}
}

func TestAMalformedPredicateIsAProblemAndTheBundledOneStays(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":           `reviewer_required = "pr.owner in ['solo'"`,
		"no such fact":     `reviewer_required = "pr.nope == 'solo'"`,
		"not a bool":       `reviewer_required = "pr.owner"`,
		"not a string":     `reviewer_required = true`,
		"a host fact":      `reviewer_required = "!pr.has_workspace"`,
		"a situation fact": `reviewer_required = "!pr.stalled"`,
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

func TestAPredicatesFileOfAPluginThatDeclaresNoneSaysSo(t *testing.T) {
	_, problems := loadProfile(t, "en", map[string]string{"plugins/slack/predicates.toml": `solo = "true"` + "\n"})
	if _, ok := problemAt(problems, "solo", "declares no predicates"); !ok {
		t.Fatalf("problems %v do not say slack declares no predicates", problems)
	}
}

func BenchmarkReviewerRequiredOverAReportChunk(b *testing.B) {
	l, problems := loadProfile(b, "en", map[string]string{
		"plugins/github/predicates.toml": `reviewer_required = "!(pr.owner in ['solo', 'lone'])"` + "\n",
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

func TestAReviewWantedThatDropsTheRequestByNameLoadsWithAWarning(t *testing.T) {
	for name, c := range map[string]struct {
		src  string
		warn bool
	}{
		"narrows to one repository":  {`pr.repo == 'acme/api' && !pr.mine`, true},
		"widens the request by name": {`pr.review_requested_me || (pr.repo == 'acme/api' && !pr.mine)`, false},
	} {
		t.Run(name, func(t *testing.T) {
			l, problems := loadProfile(t, "en", map[string]string{
				"plugins/github/predicates.toml": `review_wanted = "` + c.src + `"` + "\n",
			})
			if len(problems) > 0 {
				t.Fatalf("problems: %v", problems)
			}
			_, warned := problemAt(l.Warnings, "review_wanted", "pr.review_requested_me")
			if warned != c.warn {
				t.Fatalf("warned = %v, want %v; warnings: %v", warned, c.warn, l.Warnings)
			}
			if ok, err := l.Predicates.Holds("github", "review_wanted", map[string]any{"pr.repo": "acme/api"}); err != nil || !ok {
				t.Fatalf("review_wanted = %v, %v; the user's predicate stays in force", ok, err)
			}
		})
	}
}

func TestTheBundledProfileLoadsWithoutPredicateWarnings(t *testing.T) {
	l, _ := loadProfile(t, "en", nil)
	if _, warned := problemAt(l.Warnings, "review_wanted", ""); warned {
		t.Fatalf("the bundled review_wanted warns: %v", l.Warnings)
	}
}
