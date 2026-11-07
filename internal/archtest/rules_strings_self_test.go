package archtest

import (
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

func TestClientProseFlagsAPlantedString(t *testing.T) {
	js := `
h("button", {}, "Open cockpit");
t("Open cockpit");
h("div", {class: "row muted"});
if (e.key === "Escape") {}
el.textContent = ` + "`Synced ${x}`" + `;
// a comment that says "Nothing here"
const re = /a "quoted" b/;
el.title = 'Waiting longest';
getJSON("/api/strings");
`
	flagged(t, "js", ruleClientProse("internal/web/static/x.js", js), 3, "goes through t or tn")
	src := `package p
import (
	"errors"
	"fmt"
)
type noUnit struct{}
func (noUnit) Error() string { return "task has no unit to act on" } //prose:internal the note words it through the catalog
var items = []MenuItem{{Title: "Sweep now"}}
var keyed = c.T("Sweep now", nil)
var err = errors.New("task has no unit")
var status = fmt.Sprintf("waiting for %d things", 3)
var wrapped = fmt.Errorf("reading the plan: %w", err) //prose:internal wraps an error the caller words
func f() { panic("the window needs macOS") }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	got := ruleGoClientProse(fset, "internal/client/menubar/p.go", f)
	flagged(t, "go", got, 3, "texts.Catalog")
	for _, want := range []string{"Sweep now", "task has no unit", "waiting for %d things"} {
		if !slices.ContainsFunc(got, func(g string) bool { return strings.Contains(g, want) }) {
			t.Errorf("%q is not flagged: %q", want, got)
		}
	}
	for s, want := range map[string]bool{"Open cockpit": true, "Waiting": true, "row muted": true, "is:open waits-on:me": false, "https://x.y": false,
		".row .title": false, "Escape": false, "ok": false, "a": false, "Jan": true} {
		if got := isProse(s); got != want {
			t.Errorf("isProse(%q) = %v", s, got)
		}
	}
}

func TestHTMLProseFlagsAPlantedText(t *testing.T) {
	page := `<header><button>Do it</button><button data-i18n="Reload">Reload</button>
<input aria-label="Filter the list"><input aria-label="Views" data-i18n-aria-label="Views">
<span class="mark">{{name}}</span><script>const x = "Some words";</script><h2 data-i18n="Waiting on">Waiting on</h2></header>`
	flagged(t, "html", ruleHTMLProse("internal/web/static/index.html", page), 2, "data-i18n")
}
