package profile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoBlockHasDashFrontMatter(t *testing.T) {
	seen := 0
	for _, root := range []string{"testdata", filepath.Join("..", "..", "profiles")} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || !strings.Contains(filepath.Base(p), "intent=") {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			seen++
			if strings.HasPrefix(strings.TrimLeft(strings.TrimPrefix(string(raw), "\ufeff"), " \t\r\n"), "---\n") {
				t.Errorf("%s has --- front matter", p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen == 0 {
		t.Fatal("no block found")
	}
}

func TestTestdataBlockLoads(t *testing.T) {
	tr, err := Load([]Layer{user("testdata")}, testIntents, nil)
	if err != nil || len(tr.Blocks) != 1 {
		t.Fatalf("%+v %v", tr, err)
	}
	fm := tr.Blocks[0].FM
	if fm.Title != "Acme rules" || len(fm.Paths) != 2 || fm.Delegates["claude"][0] != "/code-review high" || len(fm.Delegates["codex"]) != 0 {
		t.Fatalf("%+v", fm)
	}
}

func TestFrontMatterErrorsCarryPositionAndHints(t *testing.T) {
	for _, c := range []struct{ fm, problem string }{
		{"paths = \"x\"", `must be a list of globs, such as ["src/**/*.go"]`},
		{"include = \"x.md\"", `must be a list of paths, such as ["policy.md"]`},
		{"override = \"yes\"", "must be true or false"},
		{"title = \"ok\"\nreasn = 1", "unknown key; the keys are override, title, include, paths and delegates"},
	} {
		d := dir(t, map[string]string{"intent=review.md": "+++\n" + c.fm + "\n+++\nX"})
		_, err := Load([]Layer{user(d)}, testIntents, nil)
		var f FrontMatterError
		if !errors.As(err, &f) || f.Problem != c.problem || f.Line < 2 || !strings.Contains(err.Error(), ":"+string(rune('0'+f.Line))+":") {
			t.Errorf("%q: %v", c.fm, err)
		}
	}
}

func TestDashFrontMatterIsRefusedInABlock(t *testing.T) {
	d := dir(t, map[string]string{"intent=review.md": "---\noverride: true\n---\nX"})
	_, err := Load([]Layer{user(d)}, testIntents, nil)
	var f FrontMatterError
	if !errors.As(err, &f) || !strings.Contains(f.Problem, "front matter is TOML between +++ lines") {
		t.Fatal(err)
	}
}

func TestAddedBlocksStartWithPlusAndReloadUnchanged(t *testing.T) {
	d := t.TempDir()
	changes, err := Addition{Intents: []Kind{Review}, Path: org("acme"), Override: true, Title: "A \"quoted\" <title>", Paths: []string{"src/**/*.go"}, Text: "Rule."}.Plan(d, testIntents)
	if err != nil || !strings.HasPrefix(string(changes[0].After), "+++\n") {
		t.Fatalf("%v %q", err, changes[0].After)
	}
	if err := Apply(d, changes, nil, testIntents, nil); err != nil {
		t.Fatal(err)
	}
	tr, err := Load([]Layer{user(d)}, testIntents, nil)
	if err != nil || len(tr.Blocks) != 1 {
		t.Fatalf("%+v %v", tr, err)
	}
	b := tr.Blocks[0]
	if !b.FM.Override || b.FM.Title != "A \"quoted\" <title>" || b.FM.Paths[0] != "src/**/*.go" || b.Text != "Rule.\n" {
		t.Fatalf("%+v", b)
	}
	again, err := Addition{Intents: []Kind{Review}, Path: org("acme"), Section: "More", Text: "Two."}.Plan(d, testIntents)
	if err != nil || !strings.HasPrefix(string(again[0].After), "+++\n") || !strings.Contains(string(again[0].After), "\n+++\nRule.") {
		t.Fatalf("%v %q", err, again[0].After)
	}
}

func TestFrontMatterChecksAfterDecodeCarryPosition(t *testing.T) {
	for _, c := range []struct{ fm, want string }{
		{"override = true\ntitle = \"  \"", "intent=review.md:3:9: front matter title: is empty"},
		{"paths = []", `intent=review.md:2:1: front matter paths: must be a list of globs, such as ["src/**/*.go"]`},
		{"include = []", `intent=review.md:2:1: front matter include: must be a list of paths, such as ["policy.md"]`},
		{"paths = [\"src/**\",\n  \"a/[b\"]", "intent=review.md:3:3: front matter paths: a/[b is not a valid glob"},
	} {
		d := dir(t, map[string]string{"intent=review.md": "+++\n" + c.fm + "\n+++\nX"})
		_, err := Load([]Layer{user(d)}, testIntents, nil)
		var f FrontMatterError
		if !errors.As(err, &f) || f.Line == 0 || !strings.HasSuffix(err.Error(), c.want) {
			t.Errorf("%q: got %v, want suffix %s", c.fm, err, c.want)
		}
	}
}
