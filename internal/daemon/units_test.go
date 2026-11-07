package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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
	t.Parallel()
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
	t.Parallel()
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

// memSkips keeps skips in memory, as the daemon's Skips.
type memSkips map[unit.Unit][]string

func (m memSkips) Skip(_ context.Context, wf string, u unit.Unit) error {
	if !slices.Contains(m[u], wf) {
		m[u] = append(m[u], wf)
	}
	return nil
}

func (m memSkips) Unskip(_ context.Context, wf string, u unit.Unit) error {
	m[u] = slices.DeleteFunc(m[u], func(w string) bool { return w == wf })
	return nil
}

func (m memSkips) Of(_ context.Context, u unit.Unit) ([]string, error) {
	return slices.Clone(m[u]), nil
}

func skipServer(t *testing.T) (*Server, memSkips, unit.Unit, string) {
	t.Helper()
	th := unit.Unit{Provider: "thread", Key: "C1/171.1"}
	sk := memSkips{}
	s := newServerWith(t, func(d *Deps) {
		d.Units = unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "thread"})
		d.RowProviders = append(d.RowProviders, func(readmodel.RowDeps) readmodel.Provider { return threadRows{units: []unit.Unit{th}} })
		d.Skips = sk
	})
	return s, sk, th, s.loaded().Workflows[0].Name
}

func TestSkipRoutesSetAndClearASkip(t *testing.T) {
	t.Parallel()
	s, sk, th, wf := skipServer(t)
	skipped := func() []string {
		t.Helper()
		var raw map[string]json.RawMessage
		if code := get(t, s, api.UnitPath(th), &raw); code != http.StatusOK {
			t.Fatalf("detail: %d", code)
		}
		var out []string
		if err := json.Unmarshal(raw["skipped_workflows"], &out); err != nil || out == nil {
			t.Fatalf("skipped_workflows %s %v, want a list", raw["skipped_workflows"], err)
		}
		return out
	}
	if got := skipped(); len(got) != 0 {
		t.Fatalf("before: %v", got)
	}
	rec := post(t, s, api.UnitPath(th, "skip", wf), "")
	var st api.UnitSkipState
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &st) != nil || st != (api.UnitSkipState{Unit: "thread/C1/171.1", Workflow: wf, Skipped: true}) {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	if got := skipped(); !slices.Equal(got, []string{wf}) || !slices.Equal(sk[th], []string{wf}) {
		t.Fatalf("after POST: %v", got)
	}
	del := httptest.NewRecorder()
	s.Handler().ServeHTTP(del, apiRequest("DELETE", api.UnitPath(th, "skip", wf), nil))
	if del.Code != 200 || json.Unmarshal(del.Body.Bytes(), &st) != nil || st.Skipped {
		t.Fatalf("DELETE: %d %s", del.Code, del.Body)
	}
	if got := skipped(); len(got) != 0 {
		t.Fatalf("after DELETE: %v", got)
	}
}

func TestSkippingAnUnknownWorkflowIsNotFound(t *testing.T) {
	t.Parallel()
	s, sk, th, _ := skipServer(t)
	if rec := post(t, s, api.UnitPath(th, "skip", "no-such-workflow"), ""); rec.Code != 404 || len(sk) != 0 {
		t.Fatalf("%d %s, skips %v", rec.Code, rec.Body, sk)
	}
}

func TestAnAgentCannotSkipAWorkflow(t *testing.T) {
	t.Parallel()
	s, sk, th, wf := skipServer(t)
	req := apiRequest("POST", api.UnitPath(th, "skip", wf), nil)
	req.Header.Set(api.SessionHeader, "term_agent1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 || len(sk) != 0 {
		t.Fatalf("%d %s, skips %v", rec.Code, rec.Body, sk)
	}
}
