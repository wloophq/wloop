package catalog

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func loadWithViews(t *testing.T, views string) *intentload.Loaded {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, intentload.ViewsFile), []byte(views), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	l, problems := intentload.Load(append(profile.BundledChain(), profile.Layer{Name: "user", Source: dir, Dir: dir}), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return l
}

func githubFacets() []api.FacetSpec { return psource.BundledCatalog(string(unit.GitHub)).Facets() }

func TestTheBundledTaskFacetsAgreeWithTheGitHubPluginsFacets(t *testing.T) {
	plugin := append(githubFacets(), psource.BundledCatalog("slack").Facets()...)
	if p := CheckFacets(bundled(t).Views.Facets, plugin); len(p) > 0 {
		t.Fatal(p.Error())
	}
}

func TestAFacetDeclaredTwiceWithOtherMechanicsIsAProblem(t *testing.T) {
	l := loadWithViews(t, "[facets.risk]\nsort = \"text\"\n")
	got := CheckFacets(l.Views.Facets, githubFacets())
	if len(got) != 1 {
		t.Fatalf("problems: %s", got.Error())
	}
	p := got[0]
	if !strings.HasSuffix(p.File, intentload.ViewsFile) || p.Line == 0 || !strings.Contains(p.Msg, "github") || !strings.Contains(p.Msg, "risk") {
		t.Errorf("problem %+v", p)
	}
}

func TestTwoPluginsMustAgreeOnAFacetToo(t *testing.T) {
	plugin := []api.FacetSpec{
		{ID: "where", Provider: "github", Group: true, Sort: "text"},
		{ID: "where", Provider: "slack", Group: true, Sort: "rank"},
	}
	got := CheckFacets(nil, plugin)
	if len(got) != 1 || !strings.Contains(got[0].Msg, "github") || !strings.Contains(got[0].Msg, "slack") {
		t.Fatalf("problems: %s", got.Error())
	}
}

func TestTwoPluginsMustRankAFacetAlike(t *testing.T) {
	plugin := []api.FacetSpec{
		{ID: "risk", Provider: "github", Group: true, Sort: "rank", Rank: []string{"low", "high"}},
		{ID: "risk", Provider: "slack", Group: true, Sort: "rank", Rank: []string{"high", "low"}},
	}
	got := CheckFacets(nil, plugin)
	if len(got) != 1 || !strings.Contains(got[0].Msg, "rank") {
		t.Fatalf("problems: %s", got.Error())
	}
}

func TestTaskFacetsServeTheirMechanics(t *testing.T) {
	specs := TaskFacets(bundled(t).Views.Facets, bundled(t).Tasks, nil)
	byID := map[string]api.FacetSpec{}
	for _, f := range specs {
		byID[f.ID] = f
	}
	moved := byID["moved"]
	if moved.Provider != "" || moved.Sort != "time" || len(moved.Buckets) != 6 || moved.Buckets[0] != (api.Bucket{ID: "1d", Label: "0-1 days", Within: "24h"}) || moved.Buckets[5].Within != "" {
		t.Errorf("moved: %+v", moved)
	}
	if w := byID["waits-on"]; w.Match != "team" || !w.Menu || w.Label == "" {
		t.Errorf("waits-on: %+v", w)
	}
	if s := byID["size"]; s.Labels["xl"] != "XL" || s.Rank[0] != "xs" {
		t.Errorf("size: %+v", s)
	}
	if s := byID["source"]; s.Labels["github"] != "GitHub" || s.Labels["slack"] != "Slack" {
		t.Errorf("source: %+v", s)
	}
	if s := byID["signal"]; !s.Group {
		t.Errorf("signal: %+v", s)
	}
}

func TestATaskFacetNamesTheFactItReads(t *testing.T) {
	facts := map[string]string{}
	for _, f := range TaskFacets(bundled(t).Views.Facets, bundled(t).Tasks, nil) {
		facts[f.ID] = f.Fact
	}
	if facts["waits-on"] != "task.owed_by" || facts["asked-by"] != "task.askers" || facts["risk"] != "brief.risk" {
		t.Errorf("facts %v", facts)
	}
	for _, f := range githubFacets() {
		if f.Fact != "" {
			t.Errorf("the plugin facet %s names the fact %q", f.ID, f.Fact)
		}
	}
}

func TestTaskFacetsTakeTheOrderOfTheirFactFromTheProfile(t *testing.T) {
	l := bundled(t)
	byID := map[string]api.FacetSpec{}
	for _, f := range TaskFacets(l.Views.Facets, l.Tasks, []string{"calls", "@decide"}) {
		byID[f.ID] = f
	}
	var states, labels []string
	for _, st := range l.Tasks.States {
		states, labels = append(states, st.Name), append(labels, st.Label)
	}
	if got := byID["state"]; !slices.Equal(got.Rank, states) || got.Labels[states[1]] != labels[1] {
		t.Errorf("state: rank %v labels %v, want the states %v labelled %v", got.Rank, got.Labels, states, labels)
	}
	if got, want := byID["category"].Rank, []string{"inbox", "ready", "deferred", "done", "dropped"}; !slices.Equal(got, want) {
		t.Errorf("category rank %v, want %v", got, want)
	}
	c := byID["contexts"]
	if !slices.Equal(c.Rank, []string{"calls", "@decide"}) || c.Labels["calls"] != "@calls" || c.Labels["@decide"] != "" {
		t.Errorf("contexts: rank %v labels %v", c.Rank, c.Labels)
	}
	if got := byID["prop.priority"].Rank; !slices.Equal(got, []string{"high", "normal", "low"}) {
		t.Errorf("a declared rank changed: %v", got)
	}
	if p, ok := l.Tasks.Prop("kind"); ok && len(p.Values) > 0 && !slices.Equal(byID["prop.kind"].Rank, p.Values) {
		t.Errorf("prop.kind rank %v, want its enum values %v", byID["prop.kind"].Rank, p.Values)
	}
}
