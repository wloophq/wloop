package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTUIViewsFromCatalog(t *testing.T) {
	m := newModel(t)
	var got []string
	for _, v := range m.views {
		got = append(got, v.ID)
	}
	if strings.Join(got, ",") != "needs-me,mine,loop" {
		t.Fatalf("tabs %v", got)
	}
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	if m.view != m.planView() {
		t.Fatalf("three tabs land on view %d, not the plan", m.view)
	}
	m.SetCatalog(api.CatalogResponse{Views: []api.ViewSpec{
		{ID: "threads", Label: "Threads", Rows: true, Surfaces: []string{"tui"}},
		{ID: "web-only", Label: "Web", Rows: true, Surfaces: []string{"web"}},
		{ID: "dash", Label: "Dash", Surfaces: []string{"tui"}},
	}})
	if len(m.views) != 1 || m.views[0] != (View{ID: "threads", Label: "Threads"}) || m.view != m.planView() {
		t.Fatalf("views %+v, view %d", m.views, m.view)
	}
}

// threadRow is a row of a provider with no payload.
func threadRow() api.Row {
	return api.Row{Unit: unit.Unit{Provider: "thread", Key: "C1/171.1"}, Link: "https://chat.example/C1/171.1", Label: "#general", Title: "Deploy window?",
		Kind: "waiting", Views: []string{"needs-me"}, Facets: map[string][]string{"channel": {"general"}},
		Summary: api.Summary{WaitsOn: "me", Since: now.Add(-5 * time.Hour), Reason: "ana asked you", Line: "2 open asks"}}
}

func TestTUIRendersAThreadRow(t *testing.T) {
	m := newModel(t)
	c := servedCatalog(t)
	c.Situations = append(c.Situations, api.SituationSpec{Provider: "thread", Kind: "waiting", Label: "waiting on you"})
	m.SetCatalog(c)
	m.SetData([]api.Row{threadRow()}, api.Health{Viewer: "me-login"})
	screen := strings.Join(m.Render(120, 30, now), "\n")
	for _, want := range []string{"#general", "Deploy window?", "me · waiting on you", "2 open asks", "ana asked you", "5h"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	for _, unwanted := range []string{"brief", "workspace", "checks"} {
		if strings.Contains(screen, "  "+unwanted+" ") {
			t.Errorf("screen shows a pull request fact %q:\n%s", unwanted, screen)
		}
	}
}

func TestFilterSearchesFacets(t *testing.T) {
	m := newModel(t)
	th := threadRow()
	gh := row(7, "Batch export", "review", "needs-me")
	gh.Facets["tag"] = []string{"lang:go"}
	m.SetData([]api.Row{th, gh}, api.Health{})
	for text, want := range map[string]unit.Unit{"general": th.Unit, "lang:go": gh.Unit, "2 open": th.Unit, "can merge": {}, "review": gh.Unit} {
		filterBy(m, text)
		got := m.visible()
		switch {
		case want == (unit.Unit{}) && len(got) != 0, want != (unit.Unit{}) && (len(got) != 1 || got[0].Unit != want):
			t.Errorf("filter %q: %v", text, got)
		}
		press(m, Key{Name: KeyEsc})
	}
}

func TestTUITabsGroupViewsByProvider(t *testing.T) {
	m := newModel(t)
	m.SetCatalog(api.CatalogResponse{Views: []api.ViewSpec{
		{ID: "needs-me", Label: "Needs me", Provider: "github", Rows: true, Surfaces: []string{"tui"}},
		{ID: "slack", Label: "Slack", Provider: "slack", Rows: true, Surfaces: []string{"tui"}},
		{ID: "mine", Label: "My PRs", Provider: "github", Rows: true, Surfaces: []string{"tui"}},
	}})
	tabs := m.tabs(200)
	needs, mine, plan, sep, slack := strings.Index(tabs, "Needs me"), strings.Index(tabs, "My PRs"), strings.Index(tabs, "Plan"),
		strings.LastIndex(tabs, "│"), strings.Index(tabs, "Slack")
	if needs < 0 || !(needs < mine && mine < plan && plan < sep && sep < slack) {
		t.Errorf("tabs %q: want GitHub's views and Plan, then a separator, then Slack", tabs)
	}
}
