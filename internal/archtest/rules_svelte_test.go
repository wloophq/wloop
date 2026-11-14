package archtest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

const webUI = "internal/web/ui/src"

// isCockpitSource says whether a file under ui/src is code the page runs: TypeScript or a component, never a
// test or a test helper.
func isCockpitSource(rel string) bool {
	if strings.HasSuffix(rel, ".test.ts") || strings.HasPrefix(rel, "testing/") {
		return false
	}
	return strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".svelte")
}

// cockpitSources are the cockpit's TypeScript modules and components under ui/src, by module-relative path.
func cockpitSources(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", webUI)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if !isCockpitSource(rel) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[webUI+"/"+rel] = string(b)
		return nil
	})
	if err != nil || len(out) == 0 {
		t.Fatalf("no sources under %s: %v", webUI, err)
	}
	return out
}

// svelteHole stands for a cut-out {…} of a component's markup: an expression, a block tag or a spread.
const svelteHole = ""

// svelteParts splits a component into its code (the script blocks and every {…} of the markup, as literals with
// their lines) and its markup with each {…} replaced by svelteHole and each script block blanked, lines kept.
func svelteParts(src string) ([]jsLiteral, string) {
	var lits []jsLiteral
	var markup strings.Builder
	line := 1
	for i := 0; i < len(src); {
		if rest := src[i:]; strings.HasPrefix(rest, "<script") {
			open := strings.IndexByte(rest, '>')
			end := strings.Index(rest, "</script>")
			if open < 0 || end < 0 {
				break
			}
			body := rest[open+1 : end]
			lits = append(lits, shifted(jsLiterals(body), line+strings.Count(rest[:open+1], "\n"))...)
			block := rest[:end+len("</script>")]
			markup.WriteString(strings.Repeat("\n", strings.Count(block, "\n")))
			line += strings.Count(block, "\n")
			i += len(block)
			continue
		}
		c := src[i]
		if c == '{' {
			end := closingBrace(src, i)
			expr := src[i+1 : end]
			lits = append(lits, shifted(jsLiterals(expr), line)...)
			markup.WriteString(svelteHole)
			markup.WriteString(strings.Repeat("\n", strings.Count(expr, "\n")))
			line += strings.Count(expr, "\n")
			i = end + 1
			continue
		}
		if c == '\n' {
			line++
		}
		markup.WriteByte(c)
		i++
	}
	return lits, markup.String()
}

func shifted(lits []jsLiteral, first int) []jsLiteral {
	for i := range lits {
		lits[i].line += first - 1
	}
	return lits
}

// closingBrace is the index of the brace that closes the one at open, braces inside the expression's string
// and template literals skipped.
func closingBrace(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; c {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		case '"', '\'', '`':
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		}
	}
	return len(src) - 1
}

// markupCodeAttrs are the attributes whose literal value is no text a person reads.
var markupCodeAttrs = []string{"class", "id", "role", "type", "name", "for", "href", "rel", "target", "autocomplete", "spellcheck",
	"inputmode", "lang", "dir", "tabindex", "maxlength", "rows", "cols", "min", "max", "step", "method", "action", "src", "width",
	"height", "viewBox", "d", "fill", "stroke", "xmlns", "aria-orientation", "aria-live", "aria-keyshortcuts", "aria-hidden",
	"aria-haspopup", "aria-controls", "aria-current", "aria-expanded", "aria-selected", "aria-pressed", "aria-busy", "aria-modal",
	"aria-atomic", "this", "slot"}

func isCodeAttr(name string) bool {
	return slices.Contains(markupCodeAttrs, name) || strings.HasPrefix(name, "data-") || strings.HasPrefix(name, "on") ||
		strings.Contains(name, ":") || strings.Contains(name, svelteHole)
}

// ruleSvelteProse flags each prose literal of a component's code outside the catalog, each prose text of its
// markup and each prose literal attribute value: in markup, a text a person reads is always an expression
// through t or tn.
func ruleSvelteProse(file, src string) []string {
	lits, markup := svelteParts(src)
	var out []string
	for _, l := range lits {
		if jsExempt.MatchString(l.before) || !isProse(l.text) {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d quotes %q; a text a person reads goes through t or tn with its key in strings.toml", file, l.line, l.text))
	}
	z := html.NewTokenizer(strings.NewReader(markup))
	line := 1
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out
		}
		raw := string(z.Raw())
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			for _, a := range tok.Attr {
				if isCodeAttr(a.Key) {
					continue
				}
				for _, part := range strings.Split(a.Val, svelteHole) {
					if isProse(part) {
						out = append(out, fmt.Sprintf("%s:%d: <%s %s=%q>; a text a person reads is an expression through t or tn", file, line, tok.Data, a.Key, a.Val))
						break
					}
				}
			}
		case html.TextToken:
			for _, part := range strings.Split(html.UnescapeString(raw), svelteHole) {
				if text := strings.TrimSpace(part); isProse(text) {
					out = append(out, fmt.Sprintf("%s:%d: markup says %q; a text a person reads is an expression through t or tn", file, line, text))
				}
			}
		}
		line += strings.Count(raw, "\n")
	}
}

// cockpitLiterals are the string literals of a cockpit source: all of a module's, a component's code's.
func cockpitLiterals(file, src string) []jsLiteral {
	if strings.HasSuffix(file, ".svelte") {
		lits, _ := svelteParts(src)
		return lits
	}
	return jsLiterals(src)
}
