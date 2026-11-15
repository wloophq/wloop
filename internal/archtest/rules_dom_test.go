package archtest

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// domRebuild are the constructs that rebuild DOM, or turn text into markup, in place of updating the nodes that
// show the data; and a component's own <style>, whose colors the token and contrast tests would not see.
var domRebuild = regexp.MustCompile(`\b(replaceChildren|replaceWith|innerHTML|outerHTML|insertAdjacentHTML)\b|\{@html\b|<style[\s>]`)

// ruleNoDOMRebuild flags each line of a cockpit source that rebuilds DOM to reflect data, or styles a component
// outside app.css.
func ruleNoDOMRebuild(file, src string) []string {
	var out []string
	for i, line := range strings.Split(src, "\n") {
		if m := domRebuild.FindString(line); m != "" {
			out = append(out, fmt.Sprintf("%s:%d uses %s; a view updates the nodes bound to the data and styles live in app.css (R-dom)", file, i+1, m))
		}
	}
	return out
}

// TestCockpitRebuildsNoDOM checks that the cockpit's modules and components never rebuild DOM to reflect data
// (no replaceChildren, replaceWith, innerHTML, outerHTML, insertAdjacentHTML or {@html}) and carry no <style>
// block, so every color stays in app.css.
func TestCockpitRebuildsNoDOM(t *testing.T) {
	for file, src := range cockpitSources(t) {
		for _, b := range ruleNoDOMRebuild(file, src) {
			t.Error(b)
		}
	}
}

func TestRDOMFlagsEachPlantedRebuild(t *testing.T) {
	src := `box.replaceChildren(a);
old.replaceWith(next);
el.innerHTML = s;
el.outerHTML = s;
el.insertAdjacentHTML("beforeend", s);
{@html text}
<style>.x { color: red }</style>
<style lang="css">
const ok = "a styled row";
el.textContent = s;
`
	flagged(t, "rebuilds", ruleNoDOMRebuild("internal/web/ui/src/X.svelte", src), 8, "(R-dom)")
	flagged(t, "the line", ruleNoDOMRebuild("x.ts", "\n\nel.innerHTML = s;"), 1, "x.ts:3")
}
