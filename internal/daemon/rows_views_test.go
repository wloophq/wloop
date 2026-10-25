package daemon

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// viewRows are threadRows in a view of their own.
type viewRows struct{ threadRows }

func (p viewRows) Derive(b readmodel.Base, in any, at time.Time) (api.Row, error) {
	r, err := p.threadRows.Derive(b, in, at)
	r.Views, r.SpecsVersion = []string{"threads"}, b.SpecsVersion
	return r, err
}

func (viewRows) Views() []api.ViewSpec {
	return []api.ViewSpec{{ID: "threads", Label: "Threads", Provider: "thread", Rows: true, Surfaces: []string{"web", "tui"}},
		{ID: "board", Label: "Board", Provider: "thread", Order: 1, Surfaces: []string{"web"}}}
}

func (viewRows) Facets() []api.FacetSpec { return nil }

func (viewRows) Situations() []api.SituationSpec { return nil }

func TestRowsServeAPluginView(t *testing.T) {
	t.Parallel()
	th := unit.Unit{Provider: "thread", Key: "C1/171.1"}
	s := newServerWith(t, func(d *Deps) {
		d.Units = unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "thread"})
		d.RowProviders = append(d.RowProviders, func(readmodel.RowDeps) readmodel.Provider { return viewRows{threadRows{units: []unit.Unit{th}}} })
	})
	var page api.RowsPage
	if code := get(t, s, api.Rows, &page); code != http.StatusOK || !slices.ContainsFunc(page.Rows, func(r api.Row) bool { return r.Unit == th && r.In("threads") }) {
		t.Fatalf("rows: %d %+v", code, page.Rows)
	}
	var acts api.ActionsResponse
	if code := get(t, s, api.Actions, &acts); code != http.StatusOK || acts.Version == "" {
		t.Fatalf("actions: %d %q", code, acts.Version)
	}
	var all api.RowsPage
	get(t, s, "/api/rows?view=all", &all)
	for _, r := range all.Rows {
		if r.SpecsVersion != acts.Version {
			t.Errorf("%s stamps %q, the actions are %q", r.Unit, r.SpecsVersion, acts.Version)
		}
	}
	var cat api.CatalogResponse
	if code := get(t, s, api.Catalog, &cat); code != http.StatusOK {
		t.Fatalf("catalog: %d", code)
	}
	web := cat.ViewsOn("web")
	n := len(prrow.ViewSpecs())
	if len(web) != n+2 || web[n].ID != "threads" || web[n+1].ID != "board" || web[0].Provider != "github" {
		t.Fatalf("web views %+v", web)
	}
}
