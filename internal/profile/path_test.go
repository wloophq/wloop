package profile

import (
	"errors"
	"slices"
	"strings"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

func TestParsePath(t *testing.T) {
	for _, c := range []struct {
		rel   string
		conds []Cond
		tags  []string
		kind  Kind
	}{
		{"intent=review.md", nil, nil, Review},
		{"organization=globex/intent=review.md", []Cond{{DimOrganization, "globex"}}, nil, Review},
		{"organization=globex/repo=widgets/intent=review.md", []Cond{{DimOrganization, "globex"}, {DimRepo, "widgets"}}, nil, Review},
		{"user=carol/intent=address.md", []Cond{{DimUser, "carol"}}, nil, Address},
		{"user=dependabot[bot]/intent=review.md", []Cond{{DimUser, "dependabot[bot]"}}, nil, Review},
		{"team=acme-platform/intent=review.md", []Cond{{DimTeam, "acme-platform"}}, nil, Review},
		{"label=hotfix/intent=review.md", []Cond{{DimLabel, "hotfix"}}, nil, Review},
		{"tags/change=refac/intent=review.md", nil, []string{"change:refac"}, Review},
		{"organization=globex/tags/change=refac/lang=go/intent=review.md", []Cond{{DimOrganization, "globex"}}, []string{"change:refac", "lang:go"}, Review},
	} {
		p, ok, err := ParsePath(c.rel, testIntents)
		if err != nil || !ok || !slices.Equal(p.Conds, c.conds) || !slices.Equal(p.Tags, c.tags) || p.Intent != c.kind {
			t.Errorf("%s: %+v %v %v", c.rel, p, ok, err)
		}
	}
	for _, rel := range []string{"README.md", "notes/x.md", "policy.txt", "review.md"} {
		if _, ok, err := ParsePath(rel, testIntents); ok || err != nil {
			t.Errorf("%s: ok %v, %v", rel, ok, err)
		}
	}
	for _, c := range []struct{ rel, problem, fix string }{
		{"tags/change=refac/review.md", "not a block name", "rename it to tags/change=refac/intent=review.md"},
		{"org=x/intent=review.md", "unknown dimension org", "organization, repo"},
		{"repo=widgets/intent=review.md", "repo is only valid right after organization", "organization=<owner>/"},
		{"user=a/organization=b/intent=review.md", "out of order", "rename it to organization=b/user=a/intent=review.md"},
		{"organisation=x/intent=review.md", "unknown dimension organisation", ""},
		{"organization=x/intent=reveiw.md", "unknown intent reveiw", "address, nudge and review"},
		{"tags/team=a/intent=review.md", "condition team comes after tags/", "rename it to team=a/intent=review.md"},
		{"tags/change=refac/user=x/intent=review.md", "condition user comes after tags/", "rename it to user=x/tags/change=refac/intent=review.md"},
		{"tags/colour=red/intent=review.md", "unknown tag key colour", "lang, framework"},
		{"organization=x/Review.md", "not a block name", "rename it to organization=x/intent=review.md"},
		{"tags/change=fix/tags/lang=go/intent=review.md", "tags appears twice", "keep one tags/ directory"},
		{"tags/lang=go/lang=go/intent=review.md", "appears twice", "drop the repeated tag"},
		{"tags/lang=go/change=fix/intent=review.md", "out of order", "rename it to tags/change=fix/lang=go/intent=review.md"},
		{"round=first/intent=address.md", "round does not apply to address", "dims list round"},
		{"reviewer=jussara-ai/intent=review.md", "reviewer does not apply to review", "dims list reviewer"},
		{"organization=x/notes.md", "not a block", "intent=<intent>.md"},
		{"tags/lang=golang/intent=review.md", "spelled lang=go", "rename it to tags/lang=go/intent=review.md"},
		{"tags/change=rewrite/intent=review.md", "not a valid value", "change is one of feature, fix"},
		{"tags/lang=-/intent=review.md", "not a valid value", "lower-case letters"},
		{"size=huge/intent=review.md", "not a valid value", "xs, s, m, l, xl"},
		{"label=a/label=b/intent=review.md", "appears twice", ""},
		{"notes/organization=x/intent=review.md", "not a dim=value condition", ""},
		{"tags/intent=review.md", "names no tag", ""},
	} {
		_, ok, err := ParsePath(c.rel, testIntents)
		var pe PathError
		if ok || !errors.As(err, &pe) || pe.File != c.rel || !strings.Contains(pe.Problem, c.problem) || !strings.Contains(pe.Fix, c.fix) || pe.Fix == "" {
			t.Errorf("%s: ok %v, %+v", c.rel, ok, err)
		}
	}
}

func TestPathValuesFoldCaseAndUnescape(t *testing.T) {
	p, ok, err := ParsePath("organization=Globex/base=release%2F1.2/intent=review.md", testIntents)
	if err != nil || !ok || !slices.Equal(p.Conds, []Cond{{DimOrganization, "globex"}, {DimBase, "release/1.2"}}) {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	rel, err := PathOf(p, testIntents)
	if err != nil || rel != "organization=globex/base=release%2F1.2/intent=review.md" {
		t.Fatalf("%q %v", rel, err)
	}
}

func TestPathOfRoundTrips(t *testing.T) {
	for _, p := range []BlockPath{
		{Intent: Nudge},
		{Conds: []Cond{{DimOrganization, "globex"}, {DimRepo, "widgets"}, {DimRound, "follow-up"}}, Intent: Review},
		{Conds: []Cond{{DimBase, "50%/off"}, {DimUser, "dependabot[bot]"}}, Tags: []string{"lang:go", "change:refac"}, Intent: Address},
		{Conds: []Cond{{DimKind, "fix"}, {DimSize, "xl"}, {DimRisk, "high"}, {DimAssociation, "first-time-contributor"}}, Intent: Review},
	} {
		rel, err := PathOf(p, testIntents)
		if err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		back, ok, err := ParsePath(rel, testIntents)
		want := sorted(p)
		if err != nil || !ok || !slices.Equal(back.Conds, want.Conds) || !slices.Equal(back.Tags, want.Tags) || back.Intent != p.Intent {
			t.Errorf("%s: %+v %v %v", rel, back, ok, err)
		}
	}
	if _, err := PathOf(BlockPath{Conds: []Cond{{DimRepo, "widgets"}}, Intent: Review}, testIntents); err == nil {
		t.Error("wrote repo without organization")
	}
	if _, err := PathOf(BlockPath{Tags: []string{"team:a"}, Intent: Review}, testIntents); err == nil {
		t.Error("wrote an unknown tag key")
	}
}

func TestRoundIsValidOnlyWhereDimsListsIt(t *testing.T) {
	intents := dintent.NewRegistry([]dintent.Def{
		{Name: "review", Profile: "review", Dims: []string{"round"}},
		{Name: "ask-author", Profile: "ask-author", Dims: []string{"round"}},
		{Name: "address", Profile: "address"},
	})
	for rel, valid := range map[string]bool{
		"round=first/intent=review.md":         true,
		"round=follow-up/intent=ask-author.md": true,
		"round=first/intent=address.md":        false,
	} {
		if _, ok, err := ParsePath(rel, intents); ok != valid || (err == nil) != valid {
			t.Errorf("%s: ok %v, %v", rel, ok, err)
		}
	}
}

func TestProbeHasNoBlocks(t *testing.T) {
	if _, ok, err := ParsePath("intent=probe.md", testIntents); ok || err == nil || !strings.Contains(err.Error(), "unknown intent probe") {
		t.Fatalf("a probe block: %v %v", ok, err)
	}
	if slices.Contains(Kinds(testIntents), "probe") {
		t.Fatal(Kinds(testIntents))
	}
}

func TestIntentBlocksForAUserIntentLoad(t *testing.T) {
	intents := dintent.NewRegistry([]dintent.Def{
		{Name: "review", Profile: "review", Dims: []string{"round"}},
		{Name: "ask-author", Profile: "ask-author"},
		{Name: "borrow", Profile: "review"},
	})
	d := dir(t, map[string]string{"organization=acme/intent=ask-author.md": "Ask kindly.", "intents/ask-author/intent.toml": "label = 1\n", "intents/ask-author/instructions.md.tmpl": "x"})
	tr, err := Load([]Layer{user(d)}, intents, nil)
	if err != nil || len(tr.Blocks) != 1 || tr.Blocks[0].Path.Intent != "ask-author" {
		t.Fatalf("%+v %v", tr.Blocks, err)
	}
	if g := tr.Compose("ask-author", Facts{Values: map[string]any{"pr.owner": "acme"}}); len(g.Parts) != 1 || !strings.Contains(g.Parts[0].Text, "Ask kindly.") {
		t.Fatalf("%+v", g)
	}
	if _, err := Load([]Layer{user(d)}, testIntents, nil); err == nil {
		t.Fatal("a block for an intent outside the registry loaded")
	}
	if _, ok, err := ParsePath("intent=borrow.md", intents); ok || err == nil {
		t.Fatal("borrow reads review's blocks; it has none of its own")
	}
}

func TestAReviewerBlockParsesUnderAddress(t *testing.T) {
	p, ok, err := ParsePath("reviewer=jussara-ai/intent=address.md", testIntents)
	if !ok || err != nil || !slices.Equal(p.Conds, []Cond{{Dim: DimReviewer, Value: "jussara-ai"}}) {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
}
