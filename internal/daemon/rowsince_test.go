package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/readmodel"
)

// liveServer is newLiveServer with the brief queue, as the rows serve it.
func liveServer(t *testing.T) (*Server, *storetest.Fixture) {
	t.Helper()
	s, f := newLiveServer(t)
	withBriefs(&s.deps, brief.New(brief.Deps{Store: f.Inventory, PRs: inventorytest.Of(f.Inventory), Taxonomy: func(github.Ref) string { return brief.DefaultTaxonomy },
		Retry: time.Hour, Changes: f.Log, Now: func() time.Time { return f.Now }, Log: t.Logf}))
	return s, f
}

// rowsPage is GET /api/rows with query, and the size of its body.
func rowsPage(t *testing.T, s *Server, query string) (api.RowsPage, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", api.Rows+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", query, rec.Code, rec.Body)
	}
	var p api.RowsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p, rec.Body.Len()
}

func sinceOf(p api.RowsPage) string { return api.Since{Epoch: p.Epoch, Rev: p.Rev}.String() }

func TestRowsSinceReturnsOnlyChanged(t *testing.T) {
	t.Parallel()
	s, f := liveServer(t)
	full, fullBytes := rowsPage(t, s, "")
	if !full.Full || len(full.Rows) < 100 {
		t.Fatalf("full page: full %v, %d rows", full.Full, len(full.Rows))
	}
	ref := f.Open[0]
	if err := inventorytest.Of(f.Inventory).SetOverride(context.Background(), dinv.Override{PR: ref, Hold: true}); err != nil {
		t.Fatal(err)
	}
	delta, deltaBytes := rowsPage(t, s, "?since="+sinceOf(full))
	if delta.Full || len(delta.Rows) != 1 || delta.Rows[0].Unit != ref.Unit() || !payload(delta.Rows[0]).Override.Hold || len(delta.Removed) != 0 {
		t.Fatalf("delta: full %v, %d rows, removed %v", delta.Full, len(delta.Rows), delta.Removed)
	}
	if delta.Epoch != full.Epoch || delta.Rev <= full.Rev {
		t.Fatalf("delta at %s, full at %s", sinceOf(delta), sinceOf(full))
	}
	if deltaBytes >= 4096 {
		t.Fatalf("a delta of one row is %d bytes; the full page %d", deltaBytes, fullBytes)
	}
	t.Logf("one changed row: %d bytes; all rows: %d bytes", deltaBytes, fullBytes)
	if same, _ := rowsPage(t, s, "?since="+sinceOf(delta)); same.Full || len(same.Rows) != 0 || len(same.Removed) != 0 || same.Rev != delta.Rev {
		t.Fatalf("nothing changed, the page holds %d rows, %d removed", len(same.Rows), len(same.Removed))
	}
}

func TestRowsSinceCarriesAChangedRow(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	before, _ := rowsPage(t, s, "")
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	if err := muteBy(s.store(), r7, s.deps.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, _ := rowsPage(t, s, "?since="+sinceOf(before))
	if after.Full || len(after.Rows) != 1 || after.Rows[0].Unit != r7.Unit() || after.Rows[0].Kind != "muted" || len(after.Removed) != 0 {
		t.Fatalf("after the mute: full %v, rows %+v, removed %v", after.Full, after.Rows, after.Removed)
	}
}

func TestRowsSinceZeroIsFull(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	for _, q := range []string{"", "?since=", "?since=0"} {
		p, _ := rowsPage(t, s, q)
		if !p.Full || len(p.Rows) != 2 || p.Epoch == "" || p.Removed == nil {
			t.Errorf("%s: full %v, %d rows, epoch %q, removed %v", q, p.Full, len(p.Rows), p.Epoch, p.Removed)
		}
	}
	for _, bad := range []string{"abc", "e1:x", ":3"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", api.Rows+"?since="+bad, nil))
		var e struct{ Error string }
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusBadRequest || !strings.HasSuffix(e.Error, "is not <epoch>:<rev>") {
			t.Errorf("since=%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
}

func TestRowsSinceFromAnotherEpochIsFull(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	full, _ := rowsPage(t, s, "")
	other := api.Since{Epoch: "another" + full.Epoch, Rev: full.Rev}
	p, _ := rowsPage(t, s, "?since="+other.String())
	if !p.Full || len(p.Rows) != len(full.Rows) || p.Epoch != full.Epoch || len(p.Removed) != 0 {
		t.Fatalf("a since of another epoch: full %v, %d rows of %d, epoch %q", p.Full, len(p.Rows), len(full.Rows), p.Epoch)
	}
}

func TestRowsSinceAheadOfRevIsFull(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	full, _ := rowsPage(t, s, "")
	ahead := api.Since{Epoch: full.Epoch, Rev: full.Rev + 5}
	p, _ := rowsPage(t, s, "?since="+ahead.String())
	if !p.Full || len(p.Rows) != len(full.Rows) || p.Rev != full.Rev {
		t.Fatalf("a since ahead of the rows: full %v, %d rows of %d, rev %d", p.Full, len(p.Rows), len(full.Rows), p.Rev)
	}
}

func TestZeroOverrideOmitted(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	r7 := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	raw := func() map[string]json.RawMessage {
		t.Helper()
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", api.Rows, nil))
		var p struct{ Rows []map[string]json.RawMessage }
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("%v %s", err, rec.Body)
		}
		for _, r := range p.Rows {
			if strings.Contains(string(r["unit"]), `"acme/api#7"`) {
				return r
			}
		}
		t.Fatalf("no acme/api#7 in %s", rec.Body)
		return nil
	}
	override := func() (json.RawMessage, bool) {
		var p map[string]json.RawMessage
		if err := json.Unmarshal(raw()["payload"], &p); err != nil {
			t.Fatal(err)
		}
		o, ok := p["override"]
		return o, ok
	}
	if o, ok := override(); ok {
		t.Fatalf("a row with no override carries %s", o)
	}
	if err := inventorytest.Of(s.store()).SetOverride(context.Background(), dinv.Override{PR: r7, Hold: true}); err != nil {
		t.Fatal(err)
	}
	var o api.Override
	if raw, _ := override(); json.Unmarshal(raw, &o) != nil || !o.Hold {
		t.Fatalf("a held row's override: %+v", o)
	}
}

func TestViewAllBytesAtLiveSize(t *testing.T) {
	t.Parallel()
	s, _ := liveServer(t)
	p, n := rowsPage(t, s, "")
	if len(p.Rows) < 100 {
		t.Fatalf("%d rows", len(p.Rows))
	}
	if n > 450<<10 {
		t.Fatalf("the rows are %d bytes for %d rows, over 450 KB", n, len(p.Rows))
	}
	t.Logf("all rows: %d bytes for %d rows", n, len(p.Rows))
}

// listedUnits is a provider whose units the test lists and drops.
type listedUnits struct {
	mu    sync.Mutex
	units []unit.Unit
}

func (p *listedUnits) set(units []unit.Unit) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.units = units
}

func (p *listedUnits) Provider() unit.ProviderID { return "thread" }

func (p *listedUnits) Listed(context.Context) ([]unit.Unit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.units), nil
}

func (p *listedUnits) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	out := map[unit.Unit]any{}
	for _, u := range units {
		out[u] = u.Key
	}
	return out, nil
}

func (p *listedUnits) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: in.(string), Views: []string{}}, nil
}

func threadsFrom(from, to int) []unit.Unit {
	var out []unit.Unit
	for i := from; i < to; i++ {
		out = append(out, unit.Unit{Provider: "thread", Key: fmt.Sprintf("t%04d", i)})
	}
	return out
}

func TestRowsSincePastTombstonesIsFull(t *testing.T) {
	t.Parallel()
	log := change.NewLog(change.DefaultWindow, nil)
	p := &listedUnits{}
	p.set(threadsFrom(0, 1100))
	s := newServer(t)
	s.modelOnce.Do(func() { s.model = readmodel.New(log, nil, nil, nil, nil, p) })
	old, _ := rowsPage(t, s, "")
	if !old.Full || len(old.Rows) != 1100 {
		t.Fatalf("full page: full %v, %d rows", old.Full, len(old.Rows))
	}
	p.set(threadsFrom(1090, 1100))
	log.Note(port.TopicPRs)
	if page, _ := rowsPage(t, s, ""); len(page.Rows) != 10 {
		t.Fatalf("%d rows after 1090 left", len(page.Rows))
	}
	p.set(threadsFrom(1095, 1100))
	log.Note(port.TopicPRs)
	behind, _ := rowsPage(t, s, "?since="+sinceOf(old))
	if !behind.Full || !slices.Equal(behind.Removed, []unit.Unit{}) || len(behind.Rows) != 5 {
		t.Fatalf("a since behind the dropped tombstones: full %v, %d rows, removed %d", behind.Full, len(behind.Rows), len(behind.Removed))
	}
	for i, r := range behind.Rows {
		if r.Unit != threadsFrom(1095, 1100)[i] {
			t.Fatalf("row %d is %s", i, r.Unit)
		}
	}
}
