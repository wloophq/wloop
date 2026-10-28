package tui

import (
	"cmp"
	"encoding/json"
	intentload "github.com/wloophq/wloop/internal/intent"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/keys"
)

const wireDir = "../../daemon/testdata/wire"

func readWire[T any](t testing.TB, name string) T {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wireDir, name))
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(brandtest.Expand(string(b)))
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func servedSpecs(t testing.TB) api.ActionsResponse {
	return readWire[api.ActionsResponse](t, "specs.json")
}

func fixtures(t testing.TB) map[string]api.Row {
	return readWire[map[string]api.Row](t, "actions.json")
}

// modelOn is a model that follows the served specs and shows the fixture row.
func modelOn(t testing.TB, fixture string) (*Model, api.Row) {
	t.Helper()
	r, ok := fixtures(t)[fixture]
	if !ok {
		t.Fatalf("no fixture %q", fixture)
	}
	r.Views = []string{"needs-me"}
	m := NewModel()
	m.SetCatalog(servedCatalog(t))
	m.SetSpecs(servedSpecs(t).Specs)
	m.SetKeys(servedKeys(servedSpecs(t)))
	m.SetData([]api.Row{r}, api.Health{})
	return m, r
}

// offering is a row of the model helpers that offers the given actions, enabled.
func offering(r api.Row, ids ...string) api.Row {
	r.Actions = nil
	for _, id := range ids {
		r.Actions = append(r.Actions, api.ActionState{ID: id, Enabled: true})
	}
	return r
}

func modelWith(t testing.TB, rows ...api.Row) *Model {
	t.Helper()
	m := NewModel()
	m.SetCatalog(servedCatalog(t))
	m.SetSpecs(servedSpecs(t).Specs)
	m.SetKeys(servedKeys(servedSpecs(t)))
	m.SetData(rows, api.Health{})
	return m
}

func newModel(t testing.TB) *Model {
	t.Helper()
	m := NewModel()
	m.SetCatalog(servedCatalog(t))
	m.SetSpecs(servedSpecs(t).Specs)
	m.SetKeys(servedKeys(servedSpecs(t)))
	return m
}

// servedCatalog is the catalog the daemon serves with the GitHub provider.
func servedCatalog(t testing.TB) api.CatalogResponse {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "readmodel", "prrow", "testdata", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c api.CatalogResponse
	if err := json.Unmarshal([]byte(brandtest.Expand(string(b))), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// setPR changes a row's pull request payload.
func setPR(r *api.Row, change func(p *prView)) {
	p := prOf(*r)
	change(&p)
	raw, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	r.Payload = raw
}

// servedKeys are the bindings GET /api/keys serves the terminal for specs:
// the core keys, the views' keys, then the actions' keys.
func servedKeys(specs api.ActionsResponse) []api.KeyView {
	help, _ := intentload.BundledKeys(os.DirFS("../../../profiles/default"))
	var out []api.KeyView
	for _, b := range keys.Core {
		if slices.Contains(b.Surfaces, keys.TUI) {
			out = append(out, api.KeyView{Key: b.Key, Action: b.Action, Context: string(b.Context), Owner: "core", Help: help.Help(string(b.Context), b.Action)})
		}
	}
	lists, _ := intentload.BundledLists(os.DirFS("../../../profiles/default"))
	var views []keys.View
	for _, id := range builtin.Views {
		views = append(views, keys.View{ID: id, Key: lists.Keys[id]})
	}
	for _, b := range keys.Views(views) {
		out = append(out, api.KeyView{Key: b.Key, Action: b.Action, Context: string(b.Context), Owner: "core", Help: lists.Views[strings.TrimPrefix(b.Action, "nav:")]})
	}
	add := func(specs []api.ActionSpec, ctx keys.Context) {
		for _, s := range specs {
			if s.Key != "" && slices.Contains(keys.SurfacesOf(s.Surfaces), keys.TUI) {
				out = append(out, api.KeyView{Key: s.Key, Action: s.ID, Context: string(ctx), Owner: cmp.Or(s.Plugin, "core"), Help: s.Label, Summary: s.Summary})
			}
		}
	}
	add(specs.Specs, keys.List)
	add(specs.Plan, keys.Plan)
	return out
}
