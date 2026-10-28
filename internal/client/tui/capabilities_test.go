package tui

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestTheTerminalReadsCapabilitiesFromTheAPI(t *testing.T) {
	m := viewsModel(t)
	m.SetCatalog(api.CatalogResponse{Views: []api.ViewSpec{
		{ID: "all", Label: "All", Rows: true, Surfaces: []string{"tui"}},
		{ID: "changes", Label: "Changes", Provider: "forge", Rows: true, Surfaces: []string{"tui"}},
		{ID: "threads", Label: "Threads", Provider: "chat", Rows: true, Surfaces: []string{"tui"}},
	}})
	m.SetPlugins([]api.Plugin{{ID: "forge", Name: "Forge", Enabled: true}, {ID: "chat", Name: "Chat", Enabled: true}, {ID: "tracker", Name: "Tracker"}})
	m.SetData(nil, api.Health{})

	m.navigate("chat")
	if k := m.viewKey(); k != "rows:threads" {
		t.Fatalf("nav:chat shows %s", k)
	}
	m.navigate("forge")
	if k := m.viewKey(); k != "rows:changes" {
		t.Fatalf("nav:forge shows %s", k)
	}
	m.navigate("tracker")
	if !strings.Contains(m.status, "no Tracker views: the plugin is disabled") {
		t.Errorf("a served plugin with no views: %q", m.status)
	}

	tabs := m.tabs(400)
	all, changes, plan, threads := strings.Index(tabs, "All"), strings.Index(tabs, "Changes"), strings.Index(tabs, planLabel), strings.Index(tabs, "Threads")
	if all < 0 || changes < 0 || plan < 0 || threads < 0 {
		t.Fatalf("tabs: %q", tabs)
	}
	if sep := strings.Index(tabs[all:threads], "│"); sep < plan-all {
		t.Errorf("the core views and the plan join the first plugin's views: %q", tabs)
	}
}
