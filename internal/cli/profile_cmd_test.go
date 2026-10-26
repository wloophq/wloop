package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func profileEnv(t *testing.T, files map[string]string) (*testEnv, string) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	d := t.TempDir()
	for rel, body := range files {
		f := filepath.Join(d, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(f), 0o700)
		os.WriteFile(f, []byte(body), 0o600)
	}
	te.cfg.Profile = d
	return te, d
}

func TestProfileAddIsADryRunByDefault(t *testing.T) {
	t.Parallel()
	te, d := profileEnv(t, nil)
	if code := te.run("profile", "add", "review,address", "--organization", "globex", "--", "Every", "PR", "follows", "xyz."); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, want := range []string{"--- /dev/null\n+++ b/organization=globex/intent=review.md\n", "+Every PR follows xyz.", "+++ b/organization=globex/intent=address.md", "dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(d, "organization=globex")); err == nil {
		t.Fatal("a dry run wrote")
	}
	te.out.Reset()
	te.In = strings.NewReader("Every PR follows xyz.\n")
	if code := te.run("profile", "add", "review", "--organization", "globex", "--apply"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	raw, _ := os.ReadFile(filepath.Join(d, "organization=globex", "intent=review.md"))
	if string(raw) != "Every PR follows xyz.\n" || !strings.Contains(te.out.String(), "backup") && !strings.Contains(te.out.String(), "wrote ") {
		t.Fatalf("%q %s", raw, te.out)
	}
}

func TestProfileAddKeepsABackupOutsideGit(t *testing.T) {
	t.Parallel()
	te, d := profileEnv(t, map[string]string{"intent=review.md": "OLD"})
	if code := te.run("profile", "add", "review", "--apply", "--", "NEW"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "backup: "+filepath.Join(d, "intent=review.md")+".loop-") {
		t.Fatal(te.out.String())
	}
	te2, d2 := profileEnv(t, map[string]string{".git/HEAD": "ref", "intent=review.md": "OLD"})
	if code := te2.run("profile", "add", "review", "--apply", "--", "NEW"); code != 0 || !strings.Contains(te2.out.String(), "review with: git -C "+d2+" status && git -C "+d2+" diff") {
		t.Fatalf("exit %d: %s", code, te2.out)
	}
}

func TestAddNormalizesTheTag(t *testing.T) {
	t.Parallel()
	te, _ := profileEnv(t, nil)
	if code := te.run("profile", "add", "review", "--tag", "lang=Golang", "--", "x"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "tag lang=Golang written as lang=go") || !strings.Contains(out, "+++ b/tags/lang=go/intent=review.md") {
		t.Fatal(out)
	}
	for _, args := range [][]string{
		{"--repo", "op", "--", "x"},
		{"--tag", "colour=red", "--", "x"},
		{"--tag", "change=rewrite", "--", "x"},
		{"--stage", "first", "--", "x"},
		{"--", "---", "x"},
	} {
		te.err.Reset()
		intent := "review"
		if args[0] == "--stage" {
			intent = "address"
		}
		if code := te.run(append([]string{"profile", "add", intent}, args...)...); code != 1 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestProfileWithoutAProfile(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Profile = ""
	if code := te.run("profile", "add", "review", "--", "x"); code != 1 || !strings.Contains(te.err.String(), "no review profile is configured; set profile in "+te.ConfigPath+" to a directory") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestProfileWhere(t *testing.T) {
	t.Parallel()
	te, d := profileEnv(t, map[string]string{"intent=review.md": "ROOT", "organization=acme/intent=review.md": "ACME", "user=ana/intent=review.md": "ANA"})
	if code := te.run("profile", "where", "review", "--organization", "acme", "--repo", "api"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, want := range []string{"layer:   " + d, "block:   organization=acme/repo=api/intent=review.md (new)", "heading: #### Repository api",
		"  bundled intent=review.md   (" + brand.Name + "'s review guide)", "  user intent=review.md   (Your review guide)", "  user organization=acme/intent=review.md   (Organization acme)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "user=ana") {
		t.Fatal(out)
	}
}

func TestProfileCheckExitsOneOnAnError(t *testing.T) {
	t.Parallel()
	te, _ := profileEnv(t, map[string]string{"intent=review.md": "R", "organization=acme/kind=fix/intent=address.md": "A"})
	if code := te.run("profile", "check"); code != 0 || !strings.Contains(te.out.String(), "address  organization=acme/kind=fix/intent=address.md   organization acme, kind fix") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	bad := t.TempDir()
	os.Mkdir(filepath.Join(bad, "organization=acme"), 0o700)
	os.WriteFile(filepath.Join(bad, "organization=acme", "review.md"), []byte("old"), 0o600)
	if code := te.run("profile", "check", bad); code != 1 || !strings.Contains(te.err.String(), "rename it to organization=acme/intent=review.md") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestProfileCheckPrintsASortedProblemForABrokenIntent(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	dir := userProfile(t, te, nil)
	src := filepath.Join("..", "intent", "testdata", "bad", "predicate-typo", "intents", "ask-author")
	for _, f := range []string{"intent.toml", "instructions.md.tmpl"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if f == "intent.toml" {
			b = append([]byte("bogus = 1\n"), b...)
		}
		if err := os.MkdirAll(filepath.Join(dir, "intents", "broken"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "intents", "broken", f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code := te.run("profile", "check")
	out, errOut := te.out.String(), te.err.String()
	if code != 1 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if at := strings.Index(out, "Intents"); at < 0 || !strings.Contains(out[at:], "ask-author") {
		t.Fatalf("the valid intent is not listed:\n%s", out)
	}
	pre := filepath.Join(dir, "intents", "broken", "intent.toml") + ":"
	if !strings.Contains(errOut, "\n"+pre) && !strings.HasPrefix(errOut, pre) {
		t.Fatalf("no problem lines of the broken manifest:\n%s", errOut)
	}
	if !strings.Contains(errOut, "pr.mien\n            ^") {
		t.Fatalf("no snippet:\n%s", errOut)
	}
	var keys []string
	for _, l := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(l, pre) {
			var line, col int
			fmt.Sscanf(strings.TrimPrefix(l, pre), "%d:%d", &line, &col)
			keys = append(keys, fmt.Sprintf("%06d:%06d", line, col))
		}
	}
	if len(keys) < 2 || !slices.IsSorted(keys) {
		t.Fatalf("problems not sorted %v:\n%s", keys, errOut)
	}
}

func TestAddFoldsTheAssociationAndRefusesASlash(t *testing.T) {
	t.Parallel()
	te, _ := profileEnv(t, nil)
	if code := te.run("profile", "add", "review", "--association", "FIRST_TIME_CONTRIBUTOR", "--", "Welcome them."); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "association FIRST_TIME_CONTRIBUTOR written as first-time-contributor") ||
		!strings.Contains(out, "+++ b/association=first-time-contributor/intent=review.md") {
		t.Fatal(out)
	}
	for _, flag := range []string{"--organization", "--user", "--team"} {
		te.err.Reset()
		if code := te.run("profile", "add", "review", flag, "a/b", "--", "x"); code != 1 || !strings.Contains(te.err.String(), "the value has no /") {
			t.Errorf("%s a/b: exit %d: %s", flag, code, te.err)
		}
	}
}

func TestAddOnAnExistingBlockNamesWhyFrontMatterIsRefused(t *testing.T) {
	t.Parallel()
	te, _ := profileEnv(t, map[string]string{"intent=review.md": "R"})
	if code := te.run("profile", "add", "review", "--paths", "src/**", "--", "x"); code != 1 || !strings.Contains(te.err.String(), "applies to every rule in a block") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}
