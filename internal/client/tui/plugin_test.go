package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/client/rowset"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const boardProvider unit.ProviderID = "fake"

// boardView lists the rows of view all and sends "nudge" for the first one
// on n; it records the keys it is given.
type boardView struct{ keys []string }

func (*boardView) ID() string    { return "fake-board" }
func (*boardView) Label() string { return "Board" }

func (*boardView) Render(set *rowset.Set, width, height int) []string {
	rows := set.View(api.ViewAll)
	out := []string{fmt.Sprintf("fake board: %d rows in %dx%d", len(rows), width, height)}
	for _, r := range rows {
		out = append(out, "  "+r.Label+"  "+r.Title)
	}
	return out
}

func (b *boardView) Key(k string, set *rowset.Set) (Command, bool) {
	b.keys = append(b.keys, k)
	rows := set.View(api.ViewAll)
	if k != "n" || len(rows) == 0 {
		return Command{}, false
	}
	return Command{Unit: rows[0].Unit, Label: rows[0].Label, Action: "nudge", Req: api.ActionRequest{Source: Source}}, true
}

func registerBoard(t *testing.T) *boardView {
	t.Helper()
	saved := registered
	t.Cleanup(func() { registered = saved })
	registered = nil
	b := &boardView{}
	RegisterView(boardProvider, b)
	return b
}

func boardPlugins(enabled bool) []api.Plugin {
	p := api.Plugin{ID: boardProvider, Enabled: enabled}
	if enabled {
		p.UI = &api.PluginUI{TUIViews: []string{"fake-board"}}
	}
	return []api.Plugin{{ID: unit.GitHub, Enabled: true}, p}
}

func TestTUIPluginView(t *testing.T) {
	b := registerBoard(t)
	m := newModel(t)
	m.SetData([]api.Row{row(7, "Batch export", "review", "needs-me"), row(9, "Retry webhooks", "checks", "mine")}, api.Health{})
	m.SetPlugins(boardPlugins(true))

	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	if m.view != m.pluginBase() {
		t.Fatalf("four tabs land on view %d, not the plugin view after the plan", m.view)
	}
	lines := m.Render(80, 12, now)
	got := strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", "plugin-view-80x12.txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs; go test -run TestTUIPluginView -update rewrites it.\ngot:\n%s", path, got)
	}

	if _, ok := press(m, Key{Rune: 'j'}); ok {
		t.Fatal("j sent a command")
	}
	c, ok := press(m, Key{Rune: 'n'})
	if !ok || c.Action != "nudge" || c.Label != "acme/api#7" {
		t.Fatalf("n in the plugin view: %+v %v", c, ok)
	}
	if strings.Join(b.keys, ",") != "j,n" {
		t.Fatalf("keys routed to the plugin view: %v", b.keys)
	}

	press(m, Key{Name: KeyTab})
	if m.view != 0 {
		t.Fatalf("tab after the plugin view lands on %d", m.view)
	}
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	if _, ok := m.plugin(); !ok {
		t.Fatalf("four tabs land on view %d", m.view)
	}

	m.SetCatalog(servedCatalog(t))
	if _, ok := m.plugin(); !ok {
		t.Fatalf("a catalog reload left the plugin view for %d", m.view)
	}

	m.SetPlugins(boardPlugins(false))
	if _, ok := m.plugin(); ok || m.view != 0 {
		t.Fatalf("a disabled plugin's view stayed: view %d", m.view)
	}
	screen := strings.Join(m.Render(120, 12, now), "\n")
	if strings.Contains(screen, "Board") {
		t.Fatalf("a disabled plugin's view is listed:\n%s", screen)
	}
	press(m, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	if m.view != 0 {
		t.Fatalf("tabs reach view %d with the plugin disabled", m.view)
	}
	press(m, Key{Rune: '5'})
	if m.view != 0 {
		t.Fatalf("5 reached view %d with the plugin disabled", m.view)
	}
}

func TestAPluginViewTheDaemonDoesNotListIsAbsent(t *testing.T) {
	registerBoard(t)
	m := newModel(t)
	ps := boardPlugins(true)
	ps[1].UI.TUIViews = nil
	m.SetPlugins(ps)
	if len(m.plugins) != 0 {
		t.Fatalf("an unlisted view mounted: %v", m.plugins)
	}
}

func TestRunShowsAnEnabledPluginsView(t *testing.T) {
	registerBoard(t)
	d := &fakeDaemon{rows: oneRow(), plugins: boardPlugins(true)}
	term, done := start(t, d)
	eventually(t, "the plugin tab", func() bool { return strings.Contains(term.screen(), "Board") })
	term.press(Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab}, Key{Name: KeyTab})
	eventually(t, "the plugin view", func() bool { return strings.Contains(term.screen(), "fake board: 1 rows") })
	quit(t, term, done)
}
