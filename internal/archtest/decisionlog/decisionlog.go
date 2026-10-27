// Package decisionlog reads the decision log's entries, one Markdown file per
// decision, and renders the index of its page.
package decisionlog

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Header starts the index table of the decision log's page.
const Header = "| Decision | What it holds |\n|---|---|\n"

// Decision is one entry of the log.
type Decision struct {
	Slug        string
	Title       string
	Description string
	// Tests are the tests its "Enforced by" line names, in order.
	Tests []string
}

const enforcedBy = "- **Enforced by**:"

var testName = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")

// Read returns the decisions of the Markdown files in dir, sorted by title.
func Read(dir string) ([]Decision, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	var out []Decision
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		d, err := parse(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		d.Slug = strings.TrimSuffix(filepath.Base(p), ".md")
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Decision) int {
		return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	return out, nil
}

func parse(src string) (Decision, error) {
	rest, ok := strings.CutPrefix(src, "---\n")
	if !ok {
		return Decision{}, fmt.Errorf("no front matter")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return Decision{}, fmt.Errorf("front matter does not end")
	}
	var d Decision
	for line := range strings.SplitSeq(front, "\n") {
		if v, ok := strings.CutPrefix(line, "title: "); ok {
			d.Title = v
		}
		if v, ok := strings.CutPrefix(line, "description: "); ok {
			d.Description = v
		}
	}
	if d.Title == "" || d.Description == "" {
		return Decision{}, fmt.Errorf("front matter needs a title and a description")
	}
	i := strings.Index(body, enforcedBy)
	if i < 0 {
		return Decision{}, fmt.Errorf("no %q line", enforcedBy)
	}
	line, _, _ := strings.Cut(body[i:], "\n")
	for _, m := range testName.FindAllStringSubmatch(line, -1) {
		d.Tests = append(d.Tests, m[1])
	}
	return d, nil
}

// Replace swaps the index table of page, from Header to the blank line that
// ends it, for one row per decision.
func Replace(page string, ds []Decision) (string, error) {
	before, rest, ok := strings.Cut(page, Header)
	if !ok {
		return "", fmt.Errorf("no table starts with %q", Header)
	}
	_, after, _ := strings.Cut(rest, "\n\n")
	var b strings.Builder
	b.WriteString(before)
	b.WriteString(Header)
	for _, d := range ds {
		fmt.Fprintf(&b, "| [%s](/architecture/decisions/%s/) | %s |\n", d.Title, d.Slug, strings.ReplaceAll(d.Description, "|", `\|`))
	}
	if after == "" {
		return b.String(), nil
	}
	return b.String() + "\n" + after, nil
}
