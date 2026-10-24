package web

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestATagValueNamedNoneIsNotTheEmptyGroup(t *testing.T) {
	rows := lensRows()[:3]
	rows[0]["brief"].(map[string]any)["tags"] = []string{"lang:none"}
	rows[1]["brief"].(map[string]any)["tags"] = []string{"lang:go"}
	var got struct {
		Groups []string `json:"groups"`
		Only   []any    `json:"only"`
		Shown  []int    `json:"shown"`
	}
	nodeRun(t, lensPrelude(t), append(slices.Clone(lensFuncs), "groupQualifier"), `const rows = `+mustJS(t, wireRows("me", time.Now(), rows...))+`;
const lens = (q, group) => ({ view: "needs-me", q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), group, sort: "", layout: "" });
const r = pipeline(rows, lens("", "tag:lang"));
process.stdout.write(JSON.stringify({
  groups: r.groups.map((g) => g.key + "(" + g.label + ")=" + g.rows.map((x) => x.ref.number).join(",")),
  only: r.groups.map(groupQualifier),
  shown: pipeline(rows, lens("tag:lang:none", "")).groups[0].rows.map((x) => x.ref.number),
}));`, nil, &got)
	if want := []string{"lang:go(lang:go)=2", "lang:none(lang:none)=1", "none(no lang)=3"}; !slices.Equal(got.Groups, want) {
		t.Errorf("groups %v, want %v", got.Groups, want)
	}
	want := []any{
		map[string]any{"field": "tag", "raw": "lang:go", "neg": false},
		map[string]any{"field": "tag", "raw": "lang:none", "neg": false},
		map[string]any{"field": "tag", "raw": "lang", "neg": true},
	}
	if !reflect.DeepEqual(got.Only, want) {
		t.Errorf("only this %v, want %v", got.Only, want)
	}
	if !slices.Equal(got.Shown, []int{1}) {
		t.Errorf("tag:lang:none shows %v", got.Shown)
	}
}

func TestEachCopyOfARowHasItsOwnFocusSlots(t *testing.T) {
	src := appJS(t)
	prelude := lensPrelude(t) + jsConst(t, src, "focusSlot") + jsConst(t, src, "CARD_GROUPS") + jsConst(t, src, "STRIP_TITLE") + jsConst(t, src, "TAG_CHIPS") +
		`function h(tag, attrs, ...c) { return { tag, attrs: attrs || {}, children: c.flat().filter((x) => x !== null && x !== undefined && x !== false) }; }
const providerName = (id) => id;
state.busy = new Set(); state.openSlot = "";
state.specs = { version: "v", byId: Object.fromEntries(["open", "hold", "mute"].map((id) => [id, { id, label: id, group: "control", surfaces: ["row"] }])) };
const CSS = { escape: (s) => s }; let asked = [];
const document = { querySelector: (sel) => { asked.push(sel); return { scrollIntoView() {} }; } };
function slots(n, out = []) {
  if (n && typeof n === "object") {
    if (n.attrs && n.attrs["data-focus"]) out.push(n.attrs["data-focus"]);
    for (const c of n.children || []) slots(c, out);
  }
  return out;
}
`
	var got struct {
		Copies [][]string `json:"copies"`
		Plain  []string   `json:"plain"`
		Cards  []string   `json:"cards"`
		Open   []string   `json:"open"`
		Asked  []string   `json:"asked"`
		Multi  []bool     `json:"multi"`
	}
	row := tagRows()[1]
	row["workspace"] = map[string]any{"id": "w1", "status": "reviewing"}
	row["actions"] = []map[string]any{{"id": "open", "enabled": true}, {"id": "hold", "enabled": true}, {"id": "mute", "enabled": true}}
	nodeRun(t, prelude, append(slices.Clone(lensFuncs), "fieldById", "foldMap", "marked", "safeURL", "where", "facet", "whoFacets", "countdown", "age", "stamp",
		"stripTitle", "mineMark", "tagChips", "briefTags", "intakeWaiting", "intakeChip", "actionStates", "actionTitle", "actionButton", "actionButtons",
		"copySlot", "multiCopy", "openRow", "openSlot", "rowTr", "card", "revealOpenRow"), `
const lens = { view: "needs-me", q: "", ts: [], quals: [], filters: new Map(), group: "tag:lang", sort: "", layout: "" };
const copies = ["lang:go", "lang:sql"].map((c) => slots(rowTr(input, "", lens, c)));
const plain = slots(rowTr(input, "", lens, ""));
const cards = ["lang:go", "lang:sql"].map((c) => card(input, "", c).children[0].attrs["data-focus"]);
state.openSlot = copies[1][0];
const open = [openSlot("github:acme/web#2"), openSlot("github:acme/web#20"), openSlot("github:acme/api#2")];
state.revealed = "";
revealOpenRow("github:acme/web#2");
process.stdout.write(JSON.stringify({ copies, plain, cards, open, asked, multi: [multiCopy("tag:lang"), multiCopy("repo")] }));`, wireRow("me", time.Now(), row), &got)
	if !slices.Equal(got.Multi, []bool{true, false}) {
		t.Errorf("multi copy %v", got.Multi)
	}
	if len(got.Copies) != 2 || len(got.Copies[0]) < 5 {
		t.Fatalf("slots %v", got.Copies)
	}
	for i, c := range []string{"lang:go", "lang:sql"} {
		for _, s := range got.Copies[i] {
			if !strings.HasPrefix(s, "row:github:acme/web#2@"+c) {
				t.Errorf("copy %s has slot %s", c, s)
			}
			if slices.Contains(got.Copies[1-i], s) {
				t.Errorf("slot %s in both copies", s)
			}
		}
	}
	if !slices.Contains(got.Plain, "row:github:acme/web#2") || !slices.Contains(got.Plain, "row:github:acme/web#2:open") {
		t.Errorf("an ungrouped row keeps its plain slots: %v", got.Plain)
	}
	if want := []string{"card:github:acme/web#2@lang:go", "card:github:acme/web#2@lang:sql"}; !slices.Equal(got.Cards, want) {
		t.Errorf("cards %v, want %v", got.Cards, want)
	}
	if want := []string{"row:github:acme/web#2@lang:sql", "", ""}; !slices.Equal(got.Open, want) {
		t.Errorf("open slot %q, want %q", got.Open, want)
	}
	if want := []string{fmt.Sprintf(`[data-focus="%s"]`, "row:github:acme/web#2@lang:sql")}; !slices.Equal(got.Asked, want) {
		t.Errorf("the open row reveal asked for %q, want %q", got.Asked, want)
	}
}
