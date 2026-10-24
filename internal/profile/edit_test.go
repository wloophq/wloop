package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func org(o string) BlockPath { return BlockPath{Conds: []Cond{{DimOrganization, o}}} }

func TestAddCreatesTheBlock(t *testing.T) {
	d := t.TempDir()
	changes, err := Addition{Intents: []Kind{Review, Address}, Path: org("Globex"), Title: "Globex rules", Paths: []string{"src/**"}, Text: "Follow xyz.\n"}.Plan(d, testIntents)
	if err != nil || len(changes) != 2 {
		t.Fatalf("%+v %v", changes, err)
	}
	c := changes[0]
	if c.Rel != "organization=globex/intent=review.md" || c.Before != nil || changes[1].Rel != "organization=globex/intent=address.md" ||
		string(c.After) != "+++\ntitle = \"Globex rules\"\npaths = [\"src/**\"]\n+++\nFollow xyz.\n" {
		t.Fatalf("%+v %q", c, c.After)
	}
	if got := Diff(c); !strings.HasPrefix(got, "--- /dev/null\n+++ b/organization=globex/intent=review.md\n@@ -0,0 +1,5 @@\n++++\n") {
		t.Fatalf("%s", got)
	}
	if _, err := os.Stat(filepath.Join(d, "organization=globex")); err == nil {
		t.Fatal("a plan wrote")
	}
	if err := Apply(d, changes, nil, testIntents, nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(c.File); string(raw) != string(c.After) {
		t.Fatalf("%q", raw)
	}
}

func TestAddAppendsUnderTheSection(t *testing.T) {
	d := dir(t, map[string]string{"intent=review.md": "Intro.\n\n## Voice\n\n- Be kind.\n\n```\n## not a heading\n```\n\n## Labels\n\n- nit\n"})
	changes, err := Addition{Intents: []Kind{Review}, Section: "voice", Text: "- Be brief."}.Plan(d, testIntents)
	if err != nil {
		t.Fatal(err)
	}
	want := "Intro.\n\n## Voice\n\n- Be kind.\n\n```\n## not a heading\n```\n\n- Be brief.\n\n## Labels\n\n- nit\n"
	if string(changes[0].After) != want {
		t.Fatalf("%q", changes[0].After)
	}
	if d := Diff(changes[0]); !strings.Contains(d, "+- Be brief.") || !strings.HasPrefix(d, "--- a/intent=review.md\n+++ b/intent=review.md\n") {
		t.Fatal(d)
	}
	changes, _ = Addition{Intents: []Kind{Review}, Text: "Last."}.Plan(d, testIntents)
	if !strings.HasSuffix(string(changes[0].After), "- nit\n\nLast.\n") {
		t.Fatalf("%q", changes[0].After)
	}
}

func TestAddAddsTheSectionAtTheEnd(t *testing.T) {
	d := dir(t, map[string]string{"intent=review.md": "+++\noverride = true\n+++\n## Voice\n\n- Be kind.\n"})
	changes, err := Addition{Intents: []Kind{Review}, Section: "Security", Text: "No secrets."}.Plan(d, testIntents)
	if err != nil || string(changes[0].After) != "+++\noverride = true\n+++\n## Voice\n\n- Be kind.\n\n## Security\n\nNo secrets.\n" {
		t.Fatalf("%q %v", changes[0].After, err)
	}
	changes, _ = Addition{Intents: []Kind{Nudge}, Section: "Tone", Text: "Short."}.Plan(d, testIntents)
	if string(changes[0].After) != "## Tone\n\nShort.\n" {
		t.Fatalf("%q", changes[0].After)
	}
}

func TestAddRefusesRepoWithoutOrg(t *testing.T) {
	_, err := Addition{Intents: []Kind{Review}, Path: BlockPath{Conds: []Cond{{DimRepo, "widgets"}}}, Text: "x"}.Plan(t.TempDir(), testIntents)
	var p PathError
	if !errors.As(err, &p) || !strings.Contains(p.Problem, "repo") {
		t.Fatal(err)
	}
}

func TestAddRefusesAnUnknownTagKey(t *testing.T) {
	if _, err := NormalizeTag("colour=red"); err == nil || !strings.Contains(err.Error(), "unknown tag key colour") {
		t.Fatal(err)
	}
	if _, err := NormalizeTag("change=rewrite"); err == nil {
		t.Fatal("accepted change=rewrite")
	}
	if got, err := NormalizeTag("lang=Golang"); err != nil || got != "lang:go" {
		t.Fatal(got, err)
	}
}

func TestAddRefusesFrontMatterTextAndRoundOutsideReview(t *testing.T) {
	d := t.TempDir()
	if _, err := (Addition{Intents: []Kind{Review}, Text: "+++\noverride = true\n+++\nX"}).Plan(d, testIntents); err == nil {
		t.Fatal("accepted a front matter fence")
	}
	if _, err := (Addition{Intents: []Kind{Address}, Path: BlockPath{Conds: []Cond{{DimRound, "first"}}}, Text: "x"}).Plan(d, testIntents); err == nil {
		t.Fatal("accepted round for address")
	}
	e := dir(t, map[string]string{"intent=review.md": "X"})
	if _, err := (Addition{Intents: []Kind{Review}, Override: true, Text: "x"}).Plan(e, testIntents); err == nil {
		t.Fatal("set front matter on an existing block")
	}
}

func TestApplyRestoresOnABrokenLayer(t *testing.T) {
	d := dir(t, map[string]string{"intent=review.md": "OLD", "organization=acme/intent=review.md": "ACME"})
	changes := []Change{
		{File: filepath.Join(d, "intent=review.md"), Rel: "intent=review.md", Before: []byte("OLD"), After: []byte("NEW")},
		{File: filepath.Join(d, "user=ana", "intent=review.md"), Rel: "user=ana/intent=review.md", After: []byte("+++\nextend = true\n+++\nX")},
	}
	var kept []string
	err := Apply(d, changes, func(p string, raw []byte) (string, error) { kept = append(kept, p); return p + ".bak", nil }, testIntents, nil)
	var f FrontMatterError
	if !errors.As(err, &f) || !strings.Contains(err.Error(), "nothing changed") {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(d, "intent=review.md")); string(raw) != "OLD" {
		t.Fatalf("%q", raw)
	}
	if _, err := os.Stat(filepath.Join(d, "user=ana")); err == nil {
		t.Fatal("the new directory stayed")
	}
	if len(kept) != 1 || kept[0] != filepath.Join(d, "intent=review.md") {
		t.Fatal(kept)
	}
}

func TestUnifiedDiff(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	after := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\n"
	want := "--- x\n+++ y\n@@ -1,5 +1,5 @@\n a\n-b\n+B\n c\n d\n e\n@@ -8,3 +8,4 @@\n h\n i\n j\n+k\n"
	if got := unified("x", "y", before, after); got != want {
		t.Fatalf("%s", got)
	}
	if unified("x", "y", "same\n", "same\n") != "" {
		t.Fatal("a diff of equal texts")
	}
}
