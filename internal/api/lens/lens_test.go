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

func TestACountdownSaysTheTimeLeftInTheRowWords(t *testing.T) {
	w := api.RowWords{CountdownSending: "sending", CountdownSeconds: "in {{.s}} s", CountdownMinutes: "in {{.m}}m {{.s}}s"}
	for _, c := range []struct {
		left time.Duration
		want string
	}{{0, "sending"}, {-time.Second, "sending"}, {4200 * time.Millisecond, "in 5 s"}, {99 * time.Second, "in 99 s"}, {125 * time.Second, "in 2m 05s"}} {
		if got := lens.Countdown(w, c.left); got != c.want {
			t.Errorf("%v left reads %q, want %q", c.left, got, c.want)
		}
	}
}

var placeWords = api.LensWords{RowWords: api.RowWords{AgeNow: "now", AgeMinutes: "{{.m}}m", AgeHours: "{{.h}}h {{.m}}m", AgeDays: "{{.d}}d {{.h}}h", More: "+{{.n}}"}}

var placeNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func TestShowWhenPlacesOnlyForTheNamedValues(t *testing.T) {
	specs := []api.FacetSpec{
		{ID: "turn", Label: "Whose turn"},
		{ID: "owed", Label: "Owed", Sort: "time", Show: "age", ShowWhen: map[string][]string{"turn": {"me"}}, ShowLabel: "since {{.at}}"},
		{ID: "moved", Label: "Moved", Sort: "time", Show: "age", ShowWhen: map[string][]string{"turn": {"others", "nobody"}}, ShowLabel: "moved {{.at}}"},
	}
	t1, t2 := "2026-11-07T09:00:00Z", "2026-11-06T12:00:00Z"
	mine := lens.Item{Facets: map[string][]string{"turn": {"me"}, "owed": {t1}, "moved": {t2}}}
	if got := lens.Parts(mine, specs, placeWords, placeNow)["age"]; len(got) != 1 || got[0].Field != "owed" {
		t.Errorf("your turn places %+v, want the owed piece", got)
	}
	theirs := lens.Item{Facets: map[string][]string{"turn": {"Others"}, "owed": {t1}, "moved": {t2}}}
	if got := lens.Parts(theirs, specs, placeWords, placeNow)["age"]; len(got) != 1 || got[0].Field != "moved" {
		t.Errorf("their turn places %+v, want the moved piece", got)
	}
	none := lens.Item{Facets: map[string][]string{"owed": {t1}, "moved": {t2}}}
	if got := lens.Parts(none, specs, placeWords, placeNow)["age"]; len(got) != 0 {
		t.Errorf("an item with no turn places %+v", got)
	}
}

func TestShowMaxCountsTheRest(t *testing.T) {
	specs := []api.FacetSpec{{ID: "tag", Label: "Tag", Show: "meta", ShowMax: 2}}
	it := lens.Item{Facets: map[string][]string{"tag": {"a", "b", "c", "d", "e"}}}
	got := lens.Parts(it, specs, placeWords, placeNow)["meta"]
	want := []lens.Piece{{Field: "tag", Value: "a", Raw: "a", Label: "a"}, {Field: "tag", Value: "b", Raw: "b", Label: "b"}, {Field: "tag", Label: "+3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	two := lens.Item{Facets: map[string][]string{"tag": {"a", "b"}}}
	if got := lens.Parts(two, specs, placeWords, placeNow)["meta"]; len(got) != 2 {
		t.Errorf("two tags place %+v, want no rest piece", got)
	}
}

func TestShowLabelCarriesItsTemplate(t *testing.T) {
	specs := []api.FacetSpec{{ID: "owed", Label: "Owed", Sort: "time", Show: "age", ShowLabel: "since {{.at}}, {{.age}}"}}
	it := lens.Item{Facets: map[string][]string{"owed": {"2026-11-07T09:00:00Z"}}}
	got := lens.Parts(it, specs, placeWords, placeNow)["age"]
	want := []lens.Piece{{Field: "owed", Value: "2026-11-07T09:00:00Z", Raw: "2026-11-07T09:00:00Z", Label: "since {{.at}}, 3h 0m", Template: "since {{.at}}, {{.age}}"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}
