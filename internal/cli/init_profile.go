package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/profiles"
)

// exampleSuffix keeps an example block out of the profile: the loader reads
// only files that end in .md.
const exampleSuffix = ".example"

// bundledBlockIntents are the intents the bundled profile has a judgment
// block for, sorted.
func bundledBlockIntents() []string {
	files, _ := fs.Glob(profiles.Default, "default/intent=*.md")
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(path.Base(f), "intent="), ".md"))
	}
	return out
}

// profileExample is an inert block for intent that shows the front matter
// keys and the condition directories, with the bundled block's headings.
func profileExample(intent string) string {
	raw, _ := fs.ReadFile(profiles.Default, "default/intent="+intent+".md")
	var headings []string
	for _, l := range strings.Split(string(raw), "\n") {
		if h, ok := strings.CutPrefix(l, "## "); ok {
			headings = append(headings, h)
		}
	}
	block := "intent=" + intent + ".md"
	var b strings.Builder
	fmt.Fprintf(&b, `+++
# An example block for the %[1]s turns. It does nothing while its name ends
# in %[3]s: rename it to %[2]s to add its text to every %[1]s turn, or move it
# into condition directories, such as organization=acme/%[2]s or
# repo=acme%%2Fapi/%[2]s, to add it only there.
# '%[4]s profile add %[1]s ...' writes a block in the right place for you, and
# '%[4]s profile check' checks this directory.
#
# title = "My %[1]s rules"
# override = true          # replace the bundled guidance above this block instead of adding to it
# paths = ["src/**/*.go"]  # only when the pull request changes one of these files
`, intent, block, exampleSuffix, brand.Command)
	if len(headings) > 0 {
		fmt.Fprintf(&b, "#\n# The bundled block has these sections: %s.\n", strings.Join(headings, "; "))
	}
	b.WriteString("+++\n## My rules\n\n- Write one rule per line, in your own words.\n")
	return b.String()
}

// writeProfileExamples creates dir with an example block per bundled intent,
// keeping every file already there; it returns the files it wrote.
func writeProfileExamples(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var wrote []string
	for _, intent := range bundledBlockIntents() {
		name := "intent=" + intent + ".md"
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			continue
		}
		file := filepath.Join(dir, name+exampleSuffix)
		if _, err := os.Stat(file); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return wrote, err
		}
		if err := os.WriteFile(file, []byte(profileExample(intent)), 0o644); err != nil {
			return wrote, err
		}
		wrote = append(wrote, file)
	}
	return wrote, nil
}

// checkProfile runs profile check on dir, quietly; the error carries its report.
func (e *Env) checkProfile(dir string) error {
	var report bytes.Buffer
	q := *e
	q.Out, q.Err = &report, &report
	if err := runProfileCheck(&q, []string{dir, "--color", "never"}); err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(report.String()))
	}
	return nil
}
