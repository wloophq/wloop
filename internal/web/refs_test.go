package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAUnitLabelStaysInTheCockpitAndTheServiceLinkLeaves(t *testing.T) {
	var catalog map[string]any
	if err := json.Unmarshal([]byte(servedCatalog(t)), &catalog); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"unit": map[string]any{"provider": "github", "key": "acme/api#8"}, "link": "https://github.com/acme/api/pull/8", "label": "acme/api#8",
		"title": "Retry flaky uploads", "kind": "review", "summary": map[string]any{"waits_on": "me", "reason": "your review is requested"},
		"views": []string{"needs-me"}, "facets": map[string][]string{}, "actions": []any{}}
	type link struct{ Class, Href, Text, Target string }
	var got map[string]struct {
		Links []link
		Text  string
	}
	nodeRun(t, cockpitScript(t), nil, `
applyCatalog(input.catalog);
applyPlugins([{ id: "github", name: "GitHub" }]);
state.health = { viewer: "me" };
const r = input.row;
state.rows = [r];
const anchors = (n, out = []) => { if (n.tag === "a") out.push({ Class: n.attrs.class || "", Href: n.attrs.href || "", Text: n.textContent, Target: n.attrs.target || "" }); (n.children || []).forEach((c) => anchors(c, out)); return out; };
const shown = (n) => ({ Links: anchors(n), Text: n.textContent });
const unitBox = unitCard({ card: { unit: r.unit, label: r.label, link: r.link } });
location.hash = "#" + routeHash("needs-me", keyOf(r));
state.detail = { row: r, events: [] };
renderDetail();
const head = document.getElementById("detail").children[0].children[0];
const plugin = document.createElement("div");
pluginDetail(plugin, r.unit);
process.stdout.write(JSON.stringify({ card: shown(unitBox), detail: shown(head), plugin: shown(plugin) }));`,
		map[string]any{"catalog": catalog, "row": row}, &got)

	service := link{Class: "ext", Href: "https://github.com/acme/api/pull/8", Text: "GitHub", Target: "_blank"}
	for _, pane := range []string{"card", "detail", "plugin"} {
		g := got[pane]
		if !slices.Contains(g.Links, service) {
			t.Errorf("%s has no service link %+v: %+v", pane, service, g.Links)
		}
		if strings.Contains(g.Text, "https://") {
			t.Errorf("%s shows a raw URL: %q", pane, g.Text)
		}
		for _, l := range g.Links {
			if l.Class == "ref" && (l.Target != "" || !strings.HasPrefix(l.Href, "#")) {
				t.Errorf("%s's unit label leaves the cockpit: %+v", pane, l)
			}
		}
	}
	if want := (link{Class: "ref", Href: "#view=all&unit=github/acme%2Fapi%238", Text: "acme/api#8"}); !slices.Contains(got["card"].Links, want) {
		t.Errorf("card links %+v, want the label to open %s", got["card"].Links, want.Href)
	}
}
