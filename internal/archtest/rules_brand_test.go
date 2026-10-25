package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand/brandtest"
)

// brandExempt are the files and directories R14 does not read: the module and
// release metadata that keep the repository name, the documentation and the
// design system.
var brandExempt = []string{".git", ".claude", "bin", "design", "docs", "internal/brand", "README.md", "SECURITY.md", "AGENTS.md", "go.mod", "go.sum", "Makefile", ".goreleaser.yaml", ".gitignore", "LICENSE"}

// fixedNames are the lines that spell the default brand in names fixed
// outside the program: the live run's Keychain item.
var fixedNames = []*regexp.Regexp{
	regexp.MustCompile(`^internal/e2e/isolated_test.go:.*"` + brandtest.DefaultName + `-e2e-slack"`),
	regexp.MustCompile(`^scripts/isolated-daemon.sh:.*"` + brandtest.DefaultName + `-e2e-slack"`),
}

// ruleR14 flags every line of content that spells the default brand, except
// the module path and the lines allowed matches.
func ruleR14(rel, content string, allowed []*regexp.Regexp) []string {
	var out []string
	for i, line := range strings.Split(content, "\n") {
		bare := strings.ReplaceAll(line, strings.TrimSuffix(module, "/"), "")
		if !brandtest.Spells(bare) {
			continue
		}
		at := rel + ":" + strings.TrimSpace(bare)
		if slicesAny(allowed, func(re *regexp.Regexp) bool { return re.MatchString(rel + ":" + bare) }) {
			continue
		}
		out = append(out, "R14: "+rel+":"+strconv.Itoa(i+1)+" spells the brand literal; use internal/brand ("+at+")")
	}
	return out
}

func slicesAny(res []*regexp.Regexp, f func(*regexp.Regexp) bool) bool {
	for _, re := range res {
		if f(re) {
			return true
		}
	}
	return false
}

// TestBrandLiteralOnlyInBrand checks that the default product name appears only
// in `internal/brand` and the exempt metadata.
func TestBrandLiteralOnlyInBrand(t *testing.T) {
	t.Parallel()
	for _, b := range scanBrand(t, filepath.Join("..", ".."), fixedNames) {
		t.Error(b)
	}
}

// scanBrand runs R14 over every file under root that brandExempt does not name.
func scanBrand(t *testing.T, root string, allowed []*regexp.Regexp) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if under(rel, brandExempt...) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, ruleR14(rel, string(raw), allowed)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestR14ReadsTheBrandConstant(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	plant := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plant("internal/x/name.go", "package x\n\nconst dir = \""+brandtest.DefaultName+"\"\n")
	plant("internal/x/env.go", "package x\n\nconst env = \""+strings.ToUpper(brandtest.DefaultName)+"_HOME\"\n")
	plant("internal/x/view.go", "package x\n\nconst ViewLoop = \"loop\"\n")
	mixed := strings.ToUpper(brandtest.DefaultName[:1]) + "L" + brandtest.DefaultName[2:]
	plant("internal/x/mixed.go", "package x\n\nconst a = \" "+mixed+"\"\n")
	plant("internal/brand/brand.go", "package brand\n\nvar Name = \""+brandtest.DefaultName+"\"\n")
	got := scanBrand(t, root, fixedNames)
	flagged(t, "planted", got, 3, "internal/x/")
	var files []string
	for _, b := range got {
		files = append(files, strings.SplitN(strings.TrimPrefix(b, "R14: "), ":", 2)[0])
	}
	if want := []string{"internal/x/env.go", "internal/x/mixed.go", "internal/x/name.go"}; strings.Join(files, " ") != strings.Join(want, " ") {
		t.Errorf("flagged %q, want %q", files, want)
	}
}

func TestR14FlagsAPlantedLiteral(t *testing.T) {
	t.Parallel()
	allowed := []*regexp.Regexp{regexp.MustCompile(`by_` + brandtest.DefaultName)}
	src := "package x\n\nvar a = \"run " + brandtest.DefaultName + "\"\nvar b = \"by_" + brandtest.DefaultName + "\"\nvar c = \"" + module + "\"\n// " + strings.ToUpper(brandtest.DefaultName) + " in capitals\n"
	got := ruleR14("internal/x/x.go", src, allowed)
	flagged(t, "planted", got, 2, "x.go:")
	if len(got) == 2 && (!strings.Contains(got[0], "x.go:3") || !strings.Contains(got[1], "x.go:6")) {
		t.Errorf("flagged lines %q, want 3 and 6", got)
	}
}
