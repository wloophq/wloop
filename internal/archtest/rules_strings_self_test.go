package archtest

import (
	"go/parser"
	"go/token"
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
import "errors"
type noUnit struct{}
func (noUnit) Error() string { return "task has no unit to act on" }
var items = []MenuItem{{Title: "Sweep now"}}
var keyed = c.T("Sweep now", nil)
var err = errors.New("task has no unit")
func f() { panic("the window needs macOS") }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	flagged(t, "go", ruleGoClientProse(fset, "internal/client/menubar/p.go", f), 1, "Sweep now")
	for s, want := range map[string]bool{"Open cockpit": true, "Waiting": true, "row muted": true, "is:open waits-on:me": false, "https://x.y": false,
		".row .title": false, "Escape": false, "ok": false, "a": false, "Jan": true} {
		if got := isProse(s); got != want {
			t.Errorf("isProse(%q) = %v", s, got)
		}
	}
}
