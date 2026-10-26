package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

func user(d string) Layer { return Layer{Name: "user", Source: d, Dir: d} }

func TestExtendIsAnUnknownKey(t *testing.T) {
	d := dir(t, map[string]string{"intent=review.md": "+++\nextend = true\n+++\nX"})
	_, err := Load([]Layer{bundled(), user(d)}, testIntents, nil)
	var f FrontMatterError
	if !errors.As(err, &f) || f.Key != "extend" || f.Problem != "unknown key" {
		t.Fatalf("%v", err)
	}
}

func TestFrontMatterKeys(t *testing.T) {
	d := dir(t, map[string]string{
		"organization=acme/intent=review.md": "+++\noverride = true\ntitle = \"Acme rules\"\npaths = [\"src/**/*.go\"]\n+++\nX",
	})
	tr, err := Load([]Layer{user(d)}, testIntents, nil)
	if err != nil || len(tr.Blocks) != 1 {
		t.Fatalf("%+v %v", tr, err)
	}
	fm := tr.Blocks[0].FM
	if !fm.Override || fm.Title != "Acme rules" || len(fm.Paths) != 1 || tr.Blocks[0].Text != "X" {
		t.Fatalf("%+v", tr.Blocks[0])
	}
	for _, bad := range []string{"title = \"\"", "paths = []", "paths = [\"[\"]", "include = \"x.md\"", "override = \"yes\""} {
		d := dir(t, map[string]string{"intent=review.md": "+++\n" + bad + "\n+++\nX"})
		var f FrontMatterError
		if _, err := Load([]Layer{user(d)}, testIntents, nil); !errors.As(err, &f) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestIncludeIsInlined(t *testing.T) {
	d := dir(t, map[string]string{
		"organization=acme/intent=review.md": "+++\ninclude = [\"../docs/policy.md\", \"note.txt\"]\n+++\nX",
		"docs/policy.md":                     "POLICY",
		"organization=acme/note.txt":         "NOTE",
	})
	tr, err := Load([]Layer{user(d)}, testIntents, nil)
	if err != nil || len(tr.Blocks) != 1 {
		t.Fatalf("%+v %v", tr, err)
	}
	inc := tr.Blocks[0].Includes
	if len(inc) != 2 || inc[0] != (Include{Name: "policy.md", Text: "POLICY"}) || inc[1] != (Include{Name: "note.txt", Text: "NOTE"}) {
		t.Fatalf("%+v", inc)
	}
	for _, bad := range []string{"[\"../../x.md\"]", "[\"missing.md\"]", "[\"script.sh\"]"} {
		d := dir(t, map[string]string{"intent=review.md": "+++\ninclude = " + bad + "\n+++\nX", "script.sh": "x"})
		var f FrontMatterError
		if _, err := Load([]Layer{user(d)}, testIntents, nil); !errors.As(err, &f) || f.Key != "include" {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestIncludesStayInsideTheProfile(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("S"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ include, problem string }{
		{`["` + outside + `"]`, "must be relative"},
		{`["link.md"]`, "not a regular file"},
		{`["dir/secret.md"]`, "not found inside the profile directory"},
	} {
		d := dir(t, map[string]string{"intent=review.md": "+++\ninclude = " + c.include + "\n+++\nX"})
		os.Symlink(outside, filepath.Join(d, "link.md"))
		os.Symlink(filepath.Dir(outside), filepath.Join(d, "dir"))
		var f FrontMatterError
		if _, err := Load([]Layer{user(d)}, testIntents, nil); !errors.As(err, &f) || !strings.Contains(f.Problem, c.problem) {
			t.Errorf("%s: %v", c.include, err)
		}
	}
}

func TestIncludesWithOneNameKeepDistinctHeadings(t *testing.T) {
	tr, _ := load(t, map[string]string{
		"intent=review.md": "+++\ninclude = [\"a/policy.md\", \"b/policy.md\", \"note.txt\"]\n+++\nX",
		"a/policy.md":      "A", "b/policy.md": "B", "note.txt": "N",
	})
	var names []string
	for _, inc := range tr.Blocks[len(tr.Blocks)-1].Includes {
		names = append(names, inc.Name)
	}
	if strings.Join(names, ",") != "a/policy.md,b/policy.md,note.txt" {
		t.Fatal(names)
	}
}

func TestTooManyIncludes(t *testing.T) {
	files := map[string]string{}
	var names []string
	for i := range IncludeMax + 1 {
		n := string(rune('a'+i)) + ".md"
		files[n] = "x"
		names = append(names, `"`+n+`"`)
	}
	files["intent=review.md"] = "+++\ninclude = [" + strings.Join(names, ",") + "]\n+++\nX"
	var f FrontMatterError
	if _, err := Load([]Layer{user(dir(t, files))}, testIntents, nil); !errors.As(err, &f) || !strings.Contains(f.Problem, "at most 8") {
		t.Fatalf("%v", err)
	}
}

func TestBlockOverTheSizeLimit(t *testing.T) {
	d := dir(t, map[string]string{
		"intent=review.md":  "+++\ninclude = [\"big.md\"]\n+++\n" + strings.Repeat("x", BlockMax/2),
		"big.md":            strings.Repeat("y", BlockMax/2+1),
		"intent=address.md": strings.Repeat("x", BlockMax),
	})
	_, err := Load([]Layer{user(d)}, testIntents, nil)
	var s SizeError
	if !errors.As(err, &s) || s.File != filepath.Join(d, "intent=review.md") {
		t.Fatalf("%v", err)
	}
}

func TestAPathErrorNamesTheFullFile(t *testing.T) {
	d := dir(t, map[string]string{"organization=acme/review.md": "X"})
	var p PathError
	if _, err := Load([]Layer{user(d)}, testIntents, nil); !errors.As(err, &p) || p.File != filepath.Join(d, "organization=acme", "review.md") || p.Fix != "rename it to organization=acme/intent=review.md" {
		t.Fatalf("%v", err)
	}
}

func TestHiddenDirectoriesAndNotesAreIgnored(t *testing.T) {
	d := dir(t, map[string]string{".git/review.md": "x", "README.md": "x", "notes/a.md": "x", "intent=nudge.md": "N"})
	tr, err := Load([]Layer{user(d)}, testIntents, nil)
	if err != nil || len(tr.Blocks) != 1 || tr.Blocks[0].Path.Intent != Nudge {
		t.Fatalf("%+v %v", tr, err)
	}
}

func TestBundledBlocksLoad(t *testing.T) {
	tr, err := Load([]Layer{bundled()}, testIntents, nil)
	if err != nil || len(tr.Blocks) != len(Kinds(testIntents)) {
		t.Fatalf("%+v %v", tr, err)
	}
	for i, b := range tr.Blocks {
		if !b.Path.Root() || b.File != "embedded "+b.Rel() || len(b.Text) < 300 {
			t.Errorf("%d: %s %+v", i, b.File, b.Path)
		}
	}
}

func TestABrokenIntentsBlocksAreSkipped(t *testing.T) {
	d := dir(t, map[string]string{"intent=review.md": "R", "organization=acme/intent=ask-author.md": "A"})
	if _, err := Load([]Layer{user(d)}, testIntents, nil); err == nil {
		t.Fatal("a block of an unknown intent loaded")
	}
	tr, err := Load([]Layer{user(d)}, testIntents, []dintent.Name{"ask-author"})
	if err != nil || len(tr.Blocks) != 1 || tr.Blocks[0].Path.Intent != "review" {
		t.Fatalf("%+v %v", tr.Blocks, err)
	}
}
