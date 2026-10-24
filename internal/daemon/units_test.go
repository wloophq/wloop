package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/readmodel"
)

func TestUnitRoutesNameTheUnit(t *testing.T) {
	s := newServer(t)
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	var d api.Detail
	if code := get(t, s, api.UnitPath(r7.Unit()), &d); code != http.StatusOK || d.Row.Unit != r7.Unit() || d.Row.Label != "acme/api#7" {
		t.Fatalf("detail: %d %+v", code, d.Row)
	}
	if rec := post(t, s, api.UnitPath(r7.Unit(), "pause"), `{"source":"cli"}`); rec.Code != http.StatusOK {
		t.Fatalf("hold: %d %s", rec.Code, rec.Body)
	}
	if code := get(t, s, api.UnitPath(r7.Unit()), &d); code != http.StatusOK || !payload(d.Row).Override.Hold {
		t.Fatalf("after hold: %d %+v", code, payload(d.Row).Override)
	}
	for _, u := range []unit.Unit{{Provider: "thread", Key: "C1/171.1"}, {Provider: unit.GitHub, Key: "acme/api"}, {Provider: unit.GitHub, Key: "acme/api#0"}} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", api.UnitPath(u), nil))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no pull request") {
			t.Errorf("%s: %d %s", u, rec.Code, rec.Body)
		}
	}
}

// threadRows serves a row titled by its key for each of units.
type threadRows struct{ units []unit.Unit }

func (threadRows) Provider() unit.ProviderID { return "thread" }

func (p threadRows) Listed(context.Context) ([]unit.Unit, error) { return p.units, nil }

func (p threadRows) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	in := map[unit.Unit]any{}
	for _, u := range units {
		in[u] = u.Key
	}
	return in, nil
}

func (threadRows) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: in.(string)}, nil
}

func TestAModuleCodecReachesTheRoute(t *testing.T) {
	th := unit.Unit{Provider: "thread", Key: "C1/171.1"}
	s := newServerWith(t, func(d *Deps) {
		d.Units = unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "thread"})
		d.RowProviders = append(d.RowProviders, func(readmodel.RowDeps) readmodel.Provider { return threadRows{units: []unit.Unit{th}} })
	})
	var d api.Detail
	if code := get(t, s, api.UnitPath(th), &d); code != http.StatusOK || d.Row.Unit != th || d.Row.Title != th.Key {
		t.Fatalf("thread detail: %d %+v", code, d.Row)
	}
	gone := unit.Unit{Provider: "thread", Key: "C9/1.1"}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", api.UnitPath(gone), nil))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "no pull request") {
		t.Fatalf("a thread with no row: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, api.UnitPath(th, "pause"), `{"source":"cli"}`); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no pull request") {
		t.Fatalf("an action on a thread: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", api.UnitPath(unit.Unit{Provider: "slack", Key: "x"}), nil))
	want := "no pull request slack:x; a unit route takes provider github and a key such as owner/repo#12, path-escaped"
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("slack:x: %d %s", rec.Code, rec.Body)
	}
}
