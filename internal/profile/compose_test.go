package profile

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func labels(g Guide) (out []string) {
	for _, p := range g.Parts {
		out = append(out, p.Label)
	}
	return out
}

func levels(g Guide) (out []int) {
	for _, p := range g.Parts {
		out = append(out, p.Level)
	}
	return out
}

func load(t *testing.T, files map[string]string) (Tree, string) {
	t.Helper()
	d := dir(t, files)
	tr, err := Load([]Layer{bundled(), user(d)}, testIntents, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tr, d
}

func block(l Layer, rel, text string, fm FrontMatter) Block {
	p, ok, err := ParsePath(rel, testIntents)
	if err != nil || !ok {
		panic(rel)
	}
	return Block{Layer: l, File: fileName(l, rel), Path: p, FM: fm, Text: text}
}

func TestTheUsersExample(t *testing.T) {
	tr, _ := load(t, map[string]string{
		"intent=review.md":                                  "Root rules.",
		"organization=globex/intent=review.md":              "+++\ninclude = [\"../docs/policy.md\"]\n+++\nFollow the compliance policy below.",
		"docs/policy.md":                                    "## Policy\nNo secrets.",
		"organization=globex/repo=widgets/intent=review.md": "New code follows hexagonal architecture.",
		"user=carol/intent=review.md":                       "Check every change against the open/closed principle.",
		"tags/change=refac/intent=review.md":                "Behavior must not change; ask for the test that proves it.",
		"organization=globex/intent=address.md":             "ADDRESS",
		"organization=other/intent=review.md":               "OTHER",
	})
	g := tr.Compose(Review, Facts{Owner: "Globex", Repo: "widgets", Author: "carol", HasBrief: true, Tags: []string{"change:refac"}})
	want := []string{brand.Name + "'s review guide", "Your review guide", "Organization globex", "Repository widgets", "User carol", "Tag change=refac"}
	if !slices.Equal(labels(g), want) || !slices.Equal(levels(g), []int{3, 3, 3, 4, 3, 3}) {
		t.Fatalf("%q %v", labels(g), levels(g))
	}
	if org := g.Parts[2].Text; org != "Follow the compliance policy below.\n\n#### policy.md\n\n##### Policy\nNo secrets." {
		t.Fatalf("%q", org)
	}
	if g.Parts[0].Path != "intent=review.md" || g.Parts[3].Path != "organization=globex/repo=widgets/intent=review.md" || len(g.Skipped) != 0 || len(g.Dropped) != 0 {
		t.Fatalf("%+v", g)
	}
	if !strings.HasPrefix(g.Parts[0].Text, "#### ") {
		t.Fatalf("the bundled guide's headings sit below its own: %.40q", g.Parts[0].Text)
	}
}

func TestBundledComesFirstAndUserExpands(t *testing.T) {
	d := t.TempDir()
	tr := Tree{Blocks: []Block{
		block(user(d), "organization=acme/intent=review.md", "MINE", FrontMatter{}),
		block(bundled(), "organization=acme/intent=review.md", "LOOP", FrontMatter{}),
		block(bundled(), "intent=review.md", "BASE", FrontMatter{}),
	}}
	g := tr.Compose(Review, Facts{Owner: "acme"})
	if texts(g) != "BASE|LOOP|MINE" || !slices.Equal(labels(g), []string{brand.Name + "'s review guide", "Organization acme (" + brand.Name + ")", "Organization acme"}) ||
		!slices.Equal(levels(g), []int{3, 3, 3}) {
		t.Fatalf("%q %q %v", texts(g), labels(g), levels(g))
	}
}

func TestNothingMatchesElsewhere(t *testing.T) {
	tr, _ := load(t, map[string]string{
		"intent=review.md":                                    "ROOT",
		"organization=acme/intent=review.md":                  "ACME",
		"organization=acme/repo=x/intent=review.md":           "X",
		"user=ana/intent=review.md":                           "ANA",
		"base=main/intent=review.md":                          "MAIN",
		"round=first/intent=review.md":                        "FIRST",
		"association=first-time-contributor/intent=review.md": "WELCOME",
	})
	g := tr.Compose(Review, Facts{Owner: "other", Repo: "x", Author: "bob", Base: "dev", Round: "follow-up", Association: "MEMBER"})
	if !slices.Equal(labels(g), []string{brand.Name + "'s review guide", "Your review guide"}) {
		t.Fatalf("%q", labels(g))
	}
	g = tr.Compose(Review, Facts{Owner: "x", Base: "Main", Round: "first", Association: "FIRST_TIME_CONTRIBUTOR"})
	if !slices.Equal(labels(g)[2:], []string{"Author association first-time-contributor", "Base branch main", "First rounds"}) {
		t.Fatalf("%q", labels(g))
	}
}

func TestMissingAncestorJoinsTheLabel(t *testing.T) {
	tr, _ := load(t, map[string]string{
		"organization=globex/repo=widgets/intent=review.md":                 "OP",
		"organization=acme/intent=review.md":                                "ACME",
		"organization=acme/repo=api/user=ana/tags/lang=go/intent=review.md": "DEEP",
	})
	g := tr.Compose(Review, Facts{Owner: "globex", Repo: "widgets"})
	if got := labels(g)[1:]; !slices.Equal(got, []string{"Organization globex, repository widgets"}) || g.Parts[1].Level != 3 {
		t.Fatalf("%q %v", got, levels(g))
	}
	g = tr.Compose(Review, Facts{Owner: "acme", Repo: "api", Author: "ana", HasBrief: true, Tags: []string{"lang:go"}})
	if got := labels(g)[1:]; !slices.Equal(got, []string{"Organization acme", "Repository api, user ana, tag lang=go"}) || !slices.Equal(levels(g)[1:], []int{3, 4}) {
		t.Fatalf("%q %v", got, levels(g))
	}
}

func TestOverrideDropsOnlyAncestors(t *testing.T) {
	d := t.TempDir()
	tr := Tree{Blocks: []Block{
		block(bundled(), "intent=review.md", "BASE", FrontMatter{}),
		block(bundled(), "organization=globex/repo=widgets/intent=review.md", "LOOP OP", FrontMatter{}),
		block(user(d), "intent=review.md", "ROOT", FrontMatter{}),
		block(user(d), "organization=globex/intent=review.md", "GLOBEX", FrontMatter{}),
		block(user(d), "organization=globex/repo=widgets/intent=review.md", "OP", FrontMatter{Override: true}),
		block(user(d), "organization=globex/repo=widgets/user=ana/intent=review.md", "OP ANA", FrontMatter{}),
		block(user(d), "organization=globex/label=hotfix/intent=review.md", "HOTFIX", FrontMatter{}),
		block(user(d), "user=ana/intent=review.md", "ANA", FrontMatter{}),
		block(user(d), "tags/change=refac/intent=review.md", "REFAC", FrontMatter{}),
	}}
	g := tr.Compose(Review, Facts{Owner: "globex", Repo: "widgets", Author: "ana", Labels: []string{"hotfix"}, HasBrief: true, Tags: []string{"change:refac"}})
	if texts(g) != "OP|OP ANA|HOTFIX|ANA|REFAC" {
		t.Fatalf("%q", texts(g))
	}
	if g.Parts[0].Label != "Organization globex, repository widgets, replaces the guidance above it" || !g.Parts[0].Override ||
		g.Parts[1].Label != "User ana" || g.Parts[1].Level != 4 || g.Parts[2].Label != "Organization globex, label hotfix" {
		t.Fatalf("%q %v", labels(g), levels(g))
	}
	if g := tr.Compose(Review, Facts{Owner: "globex", Repo: "web"}); texts(g) != "BASE|ROOT|GLOBEX" {
		t.Fatalf("the override reached another repository: %q", texts(g))
	}
}

func TestBriefDimsSkipWithoutABrief(t *testing.T) {
	tr, d := load(t, map[string]string{
		"kind=fix/intent=review.md":                    "FIX",
		"organization=acme/risk=high/intent=review.md": "RISKY",
		"organization=other/size=xl/intent=review.md":  "OTHER",
		"tags/change=refac/intent=review.md":           "REFAC",
		"organization=acme/intent=review.md":           "+++\npaths = [\"**/*.sql\"]\n+++\nSQL",
	})
	g := tr.Compose(Review, Facts{Owner: "acme"})
	if len(g.Parts) != 1 {
		t.Fatalf("%q", labels(g))
	}
	want := []string{"kind=fix/intent=review.md", "organization=acme/intent=review.md", "organization=acme/risk=high/intent=review.md", "tags/change=refac/intent=review.md"}
	for i := range want {
		want[i] = "user: " + filepath.Join(d, filepath.FromSlash(want[i]))
	}
	if !slices.Equal(g.Skipped, want) {
		t.Fatalf("%q", g.Skipped)
	}
	g = tr.Compose(Review, Facts{Owner: "acme", HasBrief: true, Kind: "fix", Risk: "low", Files: []string{"db/0001.sql"}})
	if !slices.Equal(labels(g)[1:], []string{"Organization acme", "Kind fix"}) || len(g.Skipped) != 0 {
		t.Fatalf("%q %q", labels(g), g.Skipped)
	}
}

func TestTeamAndLabelMatch(t *testing.T) {
	tr, _ := load(t, map[string]string{
		"team=acme-platform/intent=review.md": "TEAM",
		"label=hotfix/intent=review.md":       "HOTFIX",
		"label=wip/intent=review.md":          "WIP",
		"team=acme-web/intent=review.md":      "WEB",
	})
	g := tr.Compose(Review, Facts{Teams: []string{"Acme-Platform"}, Labels: []string{"HotFix", "docs"}})
	if !slices.Equal(labels(g)[1:], []string{"Team acme-platform", "Label hotfix"}) {
		t.Fatalf("%q", labels(g))
	}
}

func TestPathsFrontMatter(t *testing.T) {
	tr, _ := load(t, map[string]string{
		"intent=address.md": "+++\npaths = [\"src/**/*.go\", \"Makefile\"]\n+++\nGO",
	})
	for _, c := range []struct {
		files []string
		want  bool
	}{
		{[]string{"src/a/b/c.go"}, true},
		{[]string{"src/c.go"}, true},
		{[]string{"docs/x.md", "Makefile"}, true},
		{[]string{"docs/x.md", "lib/c.go"}, false},
		{nil, false},
	} {
		g := tr.Compose(Address, Facts{HasBrief: true, Files: c.files})
		if got := len(g.Parts) == 2; got != c.want {
			t.Errorf("%v: matched %v", c.files, got)
		}
	}
}

func TestDelegatesFromTheLastBlock(t *testing.T) {
	tr, d := load(t, map[string]string{
		"intent=review.md":                   "+++\n[delegates]\nclaude = [\"/a\"]\n+++\nROOT",
		"organization=acme/intent=review.md": "+++\n[delegates]\nclaude = [\"/b\"]\n+++\nACME",
		"user=ana/intent=review.md":          "ANA",
	})
	g := tr.Compose(Review, Facts{Owner: "acme", Author: "ana"})
	if !slices.Equal(g.Delegates["claude"], []string{"/b"}) || g.DelegatesFrom != filepath.Join(d, "organization=acme", "intent=review.md") {
		t.Fatalf("%v from %s", g.Delegates, g.DelegatesFrom)
	}
	g = tr.Compose(Review, Facts{Owner: "other"})
	if !slices.Equal(g.Delegates["claude"], []string{"/a"}) {
		t.Fatalf("%v", g.Delegates)
	}
}

func TestOverTheJudgmentLimitDropsFromTheEnd(t *testing.T) {
	big := strings.Repeat("x", 15000)
	tr, d := load(t, map[string]string{
		"intent=review.md":                            big,
		"organization=acme/intent=review.md":          big,
		"organization=acme/repo=api/intent=review.md": big,
		"user=ana/intent=review.md":                   big,
		"tags/lang=go/intent=review.md":               big,
	})
	g := tr.Compose(Review, Facts{Owner: "acme", Repo: "api", Author: "ana", HasBrief: true, Tags: []string{"lang:go"}})
	want := []string{"user: " + filepath.Join(d, "tags", "lang=go", "intent=review.md"), "user: " + filepath.Join(d, "user=ana", "intent=review.md")}
	if !slices.Equal(g.Dropped, want) || !slices.Equal(labels(g), []string{brand.Name + "'s review guide", "Your review guide", "Organization acme", "Repository api"}) {
		t.Fatalf("%q %q", g.Dropped, labels(g))
	}
	huge := Tree{Blocks: []Block{block(user(d), "intent=review.md", strings.Repeat("x", JudgmentMax+1), FrontMatter{})}}
	if g := huge.Compose(Review, Facts{}); len(g.Parts) != 1 || len(g.Dropped) != 0 {
		t.Fatalf("a root block was dropped: %q", g.Dropped)
	}
}
