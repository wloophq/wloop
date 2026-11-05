package lens_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
)

type caseTable struct {
	Now   time.Time         `json:"now"`
	Words api.LensWords     `json:"words"`
	Specs []api.FacetSpec   `json:"specs"`
	Items []lens.Item       `json:"items"`
	Cases []caseExpectation `json:"cases"`
}

type caseExpectation struct {
	Q     string `json:"q"`
	Group string `json:"group"`
	Sort  string `json:"sort"`
	Desc  bool   `json:"desc"`
	Want  struct {
		Shown  []int `json:"shown"`
		Groups []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
			Items []int  `json:"items"`
		} `json:"groups"`
	} `json:"want"`
}

func TestLensCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var table caseTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	specs := map[string]api.FacetSpec{}
	for _, s := range table.Specs {
		specs[s.ID] = s
	}
	for _, c := range table.Cases {
		q := lens.Parse(c.Q, specs)
		shown := []int{}
		for i, it := range table.Items {
			if q.Match(it, specs, table.Now) {
				shown = append(shown, i)
			}
		}
		if c.Sort != "" {
			sub := pick(table.Items, shown)
			shown = remap(shown, lens.SortBy(sub, specs[c.Sort], c.Desc, table.Now))
		}
		if !slices.Equal(shown, c.Want.Shown) {
			t.Errorf("q %q sort %q desc %v: shown %v, want %v", c.Q, c.Sort, c.Desc, shown, c.Want.Shown)
			continue
		}
		if c.Group == "" {
			continue
		}
		spec, keys, ok := lens.GroupField(c.Group, specs, table.Words)
		if !ok {
			t.Errorf("group field %q is unknown", c.Group)
			continue
		}
		var groups []lens.Group
		if keys != nil {
			groups = lens.GroupByKeys(pick(table.Items, shown), spec, keys)
		} else {
			groups = lens.GroupBy(pick(table.Items, shown), spec, table.Now)
		}
		if len(groups) != len(c.Want.Groups) {
			t.Errorf("q %q group %q: %d groups, want %d: %+v", c.Q, c.Group, len(groups), len(c.Want.Groups), groups)
			continue
		}
		for i, g := range groups {
			want := c.Want.Groups[i]
			if g.Key != want.Key || g.Label != want.Label || !slices.Equal(remap(shown, g.Items), want.Items) {
				t.Errorf("q %q group %q: group %d is %s %q %v, want %s %q %v", c.Q, c.Group, i, g.Key, g.Label, remap(shown, g.Items), want.Key, want.Label, want.Items)
			}
		}
	}
}

func pick(items []lens.Item, idx []int) []lens.Item {
	out := make([]lens.Item, len(idx))
	for i, j := range idx {
		out[i] = items[j]
	}
	return out
}

func remap(shown, idx []int) []int {
	out := make([]int, len(idx))
	for i, j := range idx {
		out[i] = shown[j]
	}
	return out
}

type partTable struct {
	Now   time.Time `json:"now"`
	Parts struct {
		Words api.RowWords    `json:"words"`
		Specs []api.FacetSpec `json:"specs"`
		Cases []struct {
			Item lens.Item               `json:"item"`
			Want map[string][]lens.Piece `json:"want"`
		} `json:"cases"`
	} `json:"parts"`
}

func TestPartsCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var table partTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Parts.Cases) == 0 {
		t.Fatal("no part cases")
	}
	for i, c := range table.Parts.Cases {
		got := lens.Parts(c.Item, table.Parts.Specs, api.LensWords{RowWords: table.Parts.Words}, table.Now)
		if !reflect.DeepEqual(got, c.Want) {
			t.Errorf("case %d: parts\n got  %+v\n want %+v", i, got, c.Want)
		}
	}
}
