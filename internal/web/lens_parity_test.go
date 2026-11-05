package web

import (
	"encoding/json"
	"reflect"
	"testing"
)

type lensWant struct {
	Shown  []int `json:"shown"`
	Groups []struct {
		Key   string `json:"key"`
		Items []int  `json:"items"`
	} `json:"groups"`
}

func TestTheCockpitLensMatchesTheGoLens(t *testing.T) {
	var table struct {
		Now   string           `json:"now"`
		Specs []map[string]any `json:"specs"`
		Items []map[string]any `json:"items"`
		Cases []struct {
			Q     string   `json:"q"`
			Group string   `json:"group"`
			Sort  string   `json:"sort"`
			Desc  bool     `json:"desc"`
			Want  lensWant `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(readFile(t, "../api/lens/testdata/cases.json"), &table); err != nil {
		t.Fatal(err)
	}
	var got []lensWant
	nodeRun(t, lensPrelude(t), lensFuncs, `
Date.now = () => Date.parse(input.now);
catalog.fields = input.specs.map(fieldOf);
process.stdout.write(JSON.stringify(input.cases.map((c) => {
  const quals = qualifiers(c.q);
  const lens = { q: c.q, ts: terms(c.q), quals, filters: filtersOf(quals), group: "", sort: c.sort ? c.sort + (c.desc ? "-desc" : "") : "", layout: "" };
  const at = (rows) => rows.map((x) => input.items.indexOf(x));
  const flat = pipelineOf(input.items, lens, null);
  const out = { shown: at(flat.groups[0].rows) };
  if (c.group) out.groups = pipelineOf(input.items, { ...lens, group: c.group }, null).groups.map((g) => ({ key: g.key, items: at(g.rows) }));
  return out;
})));`, map[string]any{"now": table.Now, "specs": table.Specs, "items": table.Items, "cases": table.Cases}, &got)
	if len(got) != len(table.Cases) {
		t.Fatalf("%d results for %d cases", len(got), len(table.Cases))
	}
	for i, c := range table.Cases {
		if !reflect.DeepEqual(got[i], c.Want) {
			t.Errorf("q %q group %q sort %q desc %v:\n got  %+v\n want %+v", c.Q, c.Group, c.Sort, c.Desc, got[i], c.Want)
		}
	}
}
