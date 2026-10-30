package web

import (
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func tagRows() []map[string]any {
	rows := lensRows()
	for i, tags := range [][]string{{"change:fix", "lang:go"}, {"lang:go", "lang:sql"}, nil, {"change:refac", "domain:billing"}, {"lang:python"}} {
		if tags == nil {
			continue
		}
		b, _ := rows[i]["brief"].(map[string]any)
		if b == nil {
			b = map[string]any{}
			rows[i]["brief"] = b
		}
		b["tags"] = tags
	}
	return rows
}

func TestTheTagQualifierAndGroupingByTagKey(t *testing.T) {
	type res struct {
		Group  string
		Groups []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
			Rows  []int  `json:"rows"`
			Only  any    `json:"only"`
		} `json:"groups"`
		Unique []int `json:"unique"`
	}
	lenses := []map[string]any{
		{"q": "tag:lang:go"},
		{"q": "tag:lang"},
		{"q": "-tag:lang:go"},
		{"q": "-tag:lang"},
		{"q": "tag:lang:go tag:lang:python"},
		{"q": "tag:lang:go -tag:change:fix"},
		{"q": "billing"},
		{"q": "TAG:Lang:Go"},
		{"q": "tag:lang repo:acme/api"},
		{"q": "", "group": "tag:lang"},
		{"q": "", "group": "tag:domain"},
	}
	var got []res
	nodeRun(t, lensPrelude(t), append(slices.Clone(lensFuncs), "groupQualifier", "groupedRows"), `const rows = `+mustJS(t, wireRows("me", time.Now(), tagRows()...))+`;
process.stdout.write(JSON.stringify(input.map((l) => {
  const lens = { view: "needs-me", q: l.q, ts: terms(l.q), quals: qualifiers(l.q), filters: filtersOf(qualifiers(l.q)), group: l.group || "", sort: "", layout: "" };
  const r = pipeline(rows, lens);
  return { Group: r.group, unique: groupedRows(r.groups).map((x) => x.ref.number),
    groups: r.groups.map((g) => ({ key: g.key, label: g.label, rows: g.rows.map((x) => x.ref.number), only: g.field ? groupQualifier(g) : null })) };
})));`, lenses, &got)
	for i, want := range [][]int{
		{2, 1},
		{2, 1, 5},
		{4, 3, 5},
		{4, 3},
		{2, 1, 5},
		{2},
		{4},
		{2, 1},
		{1, 5},
	} {
		if g := got[i].Unique; !slices.Equal(g, want) {
			t.Errorf("lens %v: rows %v, want %v", lenses[i], g, want)
		}
	}
	groups := func(r res) []string {
		var out []string
		for _, g := range r.Groups {
			out = append(out, fmt.Sprintf("%s(%s)=%v", g.Key, g.Label, g.Rows))
		}
		return out
	}
	if g, want := groups(got[9]), []string{"lang:go(lang:go)=[2 1]", "lang:python(lang:python)=[5]", "lang:sql(lang:sql)=[2]", "none(no lang)=[4 3]"}; !slices.Equal(g, want) || got[9].Group != "tag:lang" {
		t.Errorf("group by tag lang: %v (%s), want %v", g, got[9].Group, want)
	}
	if g, want := got[9].Unique, []int{2, 1, 5, 4, 3}; !slices.Equal(g, want) {
		t.Errorf("a row in two groups is listed once: %v, want %v", g, want)
	}
	if g, want := groups(got[10]), []string{"domain:billing(domain:billing)=[4]", "none(no domain)=[2 3 1 5]"}; !slices.Equal(g, want) {
		t.Errorf("group by tag domain: %v, want %v", g, want)
	}
	only := []any{got[9].Groups[0].Only, got[9].Groups[3].Only}
	want := []any{map[string]any{"field": "tag", "raw": "lang:go", "neg": false}, map[string]any{"field": "tag", "raw": "lang", "neg": true}}
	if !reflect.DeepEqual(only, want) {
		t.Errorf("only this: %v, want %v", only, want)
	}
}

func TestTagKeysListTheKeysPresentAndATagChipTogglesItsQualifier(t *testing.T) {
	var got struct {
		Keys    []string `json:"keys"`
		Fields  []any    `json:"fields"`
		GroupBy string   `json:"groupBy"`
		Toggled []string `json:"toggled"`
	}
	nodeRun(t, fieldsPrelude(t), append(slices.Clone(queryFuncs), "tagKeyField", "groupField", "tagKeys", "qualifierToken", "toggledQualifier"), `process.stdout.write(JSON.stringify({
  keys: tagKeys(input),
  fields: ["tag:lang", "tag", "tag:Lang", "tag:", "repo"].map((id) => { const f = groupField(id); return f ? [f.id, f.label, !!f.group] : null; }),
  groupBy: fieldById("tag").groupBy("lang:go"),
  toggled: [toggledQualifier("fix", "tag", "lang:go", false), toggledQualifier("tag:lang:go", "tag", "lang:go", false), toggledQualifier("tag:lang:go", "tag", "lang:go", true)],
}));`, wireRows("me", time.Now(), tagRows()...), &got)
	if !slices.Equal(got.Keys, []string{"change", "domain", "lang"}) {
		t.Errorf("keys %v", got.Keys)
	}
	want := []any{[]any{"tag:lang", "Tag lang", true}, []any{"tag", "Tag", false}, nil, nil, []any{"repo", "Repo", true}}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("fields %v, want %v", got.Fields, want)
	}
	if got.GroupBy != "tag:lang" || !slices.Equal(got.Toggled, []string{"fix tag:lang:go", "", "-tag:lang:go"}) {
		t.Errorf("group by %q, toggled %q", got.GroupBy, got.Toggled)
	}
}

func TestMyPullRequestsStandOutAndTheStripExplainsItself(t *testing.T) {
	src := appJS(t)
	for _, want := range []string{`mine ? "mine" : ""`, `title: stripTitle(r)`, `mine ? mineMark() : null`, "stripLegend(),"} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	raw, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, want := range []string{"tbody tr.mine { background: var(--mine-bg); }", ".card.mine { background: var(--mine-bg); }", ".mine-mark {", ".legend .sw {"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	if strings.Index(css, "tbody tr.mine {") > strings.Index(css, "tbody tr.sel,") || strings.Index(css, ".card.mine {") > strings.Index(css, ".card.sel,") {
		t.Error("the mine tint must come before the selection, so the open row still shows as open")
	}
	prelude := `function h(tag, attrs, ...c) { return { tag, attrs, children: c.flat().filter((x) => x !== null && x !== undefined && x !== false) }; }` + "\n" +
		jsConst(t, src, "SIZES") + jsConst(t, src, "RISKS") + jsConst(t, src, "TAG_CHIPS") + keyJS(t) + catalogPrelude(t) +
		`const state = { health: { viewer: "Me" } }; function writeHash() {}` + "\n"
	var got struct {
		Cards  []map[string]any `json:"cards"`
		Titles []string         `json:"titles"`
		Legend map[string]any   `json:"legend"`
	}
	row := func(n int, author, kind, waiting, reason string) map[string]any {
		return map[string]any{"ref": map[string]any{"owner": "acme", "repo": "api", "number": n}, "author": author, "kind": kind, "waiting_on": waiting, "reason": reason}
	}
	rows := []map[string]any{
		row(1, "me", "changes", "author", "changes requested by bia"),
		row(2, "ana", "changes", "author", "changes requested by bia"),
		row(3, "ana", "review", "bia, team:acme/core", ""),
		row(4, "ana", "loop_working", "me", "a review round runs"),
		row(5, "ana", "draft", "nobody", ""),
	}
	nodeRun(t, prelude, []string{"isSet", "age", "stamp", "resolveWho", "waitingLabel", "waitField", "waitCat", "waitClass", "isMine", "stripTitle",
		"intakeWaiting", "intakeChip", "tagChips", "briefTags", "mineMark", "copySlot", "card", "stripLegend"}, `process.stdout.write(JSON.stringify({
  cards: input.map((r) => card(r, "").children[0].attrs),
  titles: input.map(stripTitle),
  legend: stripLegend(),
}));`, wireRows("Me", time.Now(), rows...), &got)
	classes := func(i int) []string { return strings.Fields(got.Cards[i]["class"].(string)) }
	if !slices.Contains(classes(0), "mine") || slices.Contains(classes(1), "mine") {
		t.Errorf("mine class: %v / %v", classes(0), classes(1))
	}
	wantTitles := []string{
		"Waiting on me: changes asked. changes requested by bia",
		"Waiting on the author (ana): changes asked. changes requested by bia",
		"Waiting on a reviewer (bia, team:acme/core): review",
		"In loop: agent working. a review round runs",
		"Waiting on nobody: draft",
	}
	if !slices.Equal(got.Titles, wantTitles) {
		t.Errorf("titles\n%q\nwant\n%q", got.Titles, wantTitles)
	}
	if got.Cards[1]["title"] != wantTitles[1] {
		t.Errorf("card title %v", got.Cards[1]["title"])
	}
	legend := mustJS(t, got.Legend)
	for _, want := range []string{`"Waits on (role):"`, `"lg w-me"`, `"Waiting on me"`, `"lg w-author"`, `"lg w-reviewer"`, `"lg w-loop"`, `"lg w-nobody"`, `"Waiting on nobody"`} {
		if !strings.Contains(legend, want) {
			t.Errorf("legend lacks %s: %s", want, legend)
		}
	}
}

func TestTheTypeQualifierFiltersAndGroupsByTheBriefKind(t *testing.T) {
	rows := lensRows()
	for i, kind := range []string{"feature", "fix", "", "chore", "feature"} {
		if b, _ := rows[i]["brief"].(map[string]any); b != nil && kind != "" {
			b["kind"] = kind
		}
	}
	type res struct {
		Rows   []int    `json:"rows"`
		Groups []string `json:"groups"`
	}
	var got []res
	nodeRun(t, lensPrelude(t), lensFuncs, `const rows = `+mustJS(t, wireRows("me", time.Now(), rows...))+`;
process.stdout.write(JSON.stringify(input.map((l) => {
  const lens = { view: "needs-me", q: l.q, ts: terms(l.q), quals: qualifiers(l.q), filters: filtersOf(qualifiers(l.q)), group: l.group || "", sort: "", layout: "" };
  const r = pipeline(rows, lens);
  return { rows: r.groups.flatMap((g) => g.rows.map((x) => x.ref.number)), groups: r.groups.map((g) => g.label) };
})));`, []map[string]any{{"q": "type:feature"}, {"q": "-type:feature"}, {"q": "type:fix type:chore"}, {"q": "", "group": "type"}}, &got)
	sorted := func(xs []int) []int { return slices.Sorted(slices.Values(xs)) }
	for i, want := range [][]int{{1, 5}, {2, 3, 4}, {2, 4}} {
		if !slices.Equal(sorted(got[i].Rows), want) {
			t.Errorf("lens %d: rows %v, want %v", i, got[i].Rows, want)
		}
	}
	if g := got[3].Groups; !slices.Contains(g, "feature") || !slices.Contains(g, "no brief") {
		t.Errorf("groups by type: %v", g)
	}
}
