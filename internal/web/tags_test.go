package web

import (
	"encoding/json"
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
	for i, tags := range [][]string{{"change:fix", "lang:go", "urgent"}, {"lang:go", "lang:sql"}, nil, {"change:refac", "domain:billing"}, {"lang:python"}} {
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
		{"q": "tag:lang where:acme/api"},
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
	nodeRun(t, fieldsPrelude(t), append(slices.Clone(queryFuncs), "tagKeyField", "groupField", "tagKeys", "qualifierToken", "toggledQualifier", "lensWords", "fillWord"), `globalThis.tasksState = { catalog: { lens: { tag_key: "{{.label}} {{.key}}", tag_key_empty: "no {{.key}}" } } };
process.stdout.write(JSON.stringify({
  keys: tagKeys(input),
  fields: ["tag:lang", "tag", "tag:Lang", "tag:", "where"].map((id) => { const f = groupField(id); return f ? [f.id, f.label, !!f.group] : null; }),
  groupBy: fieldById("tag").groupBy("lang:go"),
  toggled: [toggledQualifier("fix", "tag", "lang:go", false), toggledQualifier("tag:lang:go", "tag", "lang:go", false), toggledQualifier("tag:lang:go", "tag", "lang:go", true)],
}));`, wireRows("me", time.Now(), tagRows()...), &got)
	if !slices.Equal(got.Keys, []string{"change", "domain", "lang"}) {
		t.Errorf("keys %v", got.Keys)
	}
	want := []any{[]any{"tag:lang", "Tag lang", true}, []any{"tag", "Tag", false}, nil, nil, []any{"where", "Where", true}}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Errorf("fields %v, want %v", got.Fields, want)
	}
	if got.GroupBy != "tag:lang" || !slices.Equal(got.Toggled, []string{"fix tag:lang:go", "", "-tag:lang:go"}) {
		t.Errorf("group by %q, toggled %q", got.GroupBy, got.Toggled)
	}
}

func TestMyPullRequestsStandOutAndTheStripExplainsItself(t *testing.T) {
	raw, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, want := range []string{".row.mine { background: var(--mine-bg); }", ".card.mine { background: var(--mine-bg); }", ".legend .sw {"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	if strings.Index(css, ".row.mine {") > strings.Index(css, ".row.sel,") || strings.Index(css, ".card.mine {") > strings.Index(css, ".card.sel,") {
		t.Error("the mine tint must come before the selection, so the open row still shows as open")
	}
	var got struct {
		Cards  []map[string]string `json:"cards"`
		Rows   []string            `json:"rows"`
		Legend map[string]any      `json:"legend"`
	}
	row := func(n int, author, kind, waiting, reason string) map[string]any {
		return map[string]any{"ref": map[string]any{"owner": "acme", "repo": "api", "number": n}, "author": author, "kind": kind, "waiting_on": waiting, "reason": reason,
			"views": []string{"mine"}}
	}
	rows := []map[string]any{
		row(1, "me", "changes", "author", "changes requested by bia"),
		row(2, "ana", "changes", "author", "changes requested by bia"),
		row(3, "ana", "review", "bia, team:acme/core", ""),
		row(4, "ana", "loop_working", "me", "a review round runs"),
		row(5, "ana", "draft", "nobody", ""),
	}
	rowViewRun(t, json.RawMessage(servedCatalog(t)), `state.rows = input.rows;
const lens = lensOf({ ...readHash(), view: "mine" });
const openOf = (n) => (n.attrs.class === "open" ? n : n.children.map((c) => (c && c.attrs ? openOf(c) : null)).find(Boolean));
process.stdout.write(JSON.stringify({
  cards: input.rows.map((r) => { const c = rowView(rowModel(r, "", lens, "", "card"), lens, { card: true }); return { class: c.attrs.class, title: openOf(c).attrs.title }; }),
  rows: input.rows.map((r) => rowView(rowModel(r, "", lens, ""), lens).attrs.title),
  legend: stripLegend({ stripSpec: viewStrip("mine") }),
}));`, map[string]any{"rows": wireRows("me", time.Now(), rows...), "now": time.Now().UnixMilli()}, &got)
	classes := func(i int) []string { return strings.Fields(got.Cards[i]["class"]) }
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
	for i, want := range wantTitles {
		if got.Cards[i]["title"] != want || got.Rows[i] != want {
			t.Errorf("row %d's card reads %q and its list row %q, want %q", i+1, got.Cards[i]["title"], got.Rows[i], want)
		}
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
